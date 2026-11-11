package swarm

import (
	"errors"
	"fmt"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestDockerDiagnosticFaults(t *testing.T) {
	for _, tc := range []struct {
		diagnostic string
		code       faults.Code
		summary    string
	}{
		{diagnostic: "no suitable node (scheduling constraints not satisfied on 3 nodes)", code: faults.CodeNoSuitableNode},
		{diagnostic: "no suitable node (insufficient resources on 1 node)", code: faults.CodeNoSuitableNode},
		{diagnostic: "no suitable node (1 node not available for new tasks)", code: faults.CodeNoSuitableNode},
		{diagnostic: "unauthorized: authentication required", code: faults.CodeImagePullDenied},
		{diagnostic: "denied: requested access to the resource is denied", code: faults.CodeImagePullDenied},
		{diagnostic: "no basic auth credentials", code: faults.CodeImagePullDenied},
		{diagnostic: "insufficient_scope: authorization failed", code: faults.CodeImagePullDenied},
		{diagnostic: `invalid mount config for type "bind": bind source path does not exist: /srv/data`, code: faults.CodeMountFailed},
		{diagnostic: "error while creating mount source path '/srv/data': mkdir /srv/data: read-only file system", code: faults.CodeMountFailed},
		{diagnostic: "error while mounting volume '/var/lib/docker/volumes/data/_data': permission denied", code: faults.CodeMountFailed},
		{diagnostic: "error while creating volume 'data': VolumeDriver.Create: backend unavailable", code: faults.CodeMountFailed},
		{diagnostic: "VolumeDriver.Mount: mount operation failed", code: faults.CodeMountFailed},
		{diagnostic: "failed to mount local volume: mount :/data:/var/lib/docker/volumes/data/_data: permission denied", code: faults.CodeMountFailed},
		{diagnostic: `error mounting "/srv/data" to rootfs at "/data": permission denied`, code: faults.CodeMountFailed},
		{diagnostic: "mount failed: exit status 32", code: faults.CodeMountFailed},
		{
			diagnostic: `failed to prepare extraction snapshot "extract-225092350-0LGf sha256:95dbfedee91178a3278e2e146a03bfd2223378ee9f60480b71df6f8d6cc1316e": NotFound: lease does not exist: not found`,
			code:       faults.CodeImageExtractionFailed,
			summary:    "Container image extraction failed: containerd snapshot lease is missing. Retry deployment; check for concurrent image cleanup.",
		},
	} {
		t.Run(tc.diagnostic, func(t *testing.T) {
			original := errors.New(tc.diagnostic)
			cause := fmt.Errorf("Error response from daemon: %w", original)
			classified := classifyDockerError(cause)
			var coded faults.Coded
			require.ErrorAs(t, classified, &coded)
			assert.Equal(t, tc.code, coded.FaultCode())
			wantSummary := tc.diagnostic
			if tc.summary != "" {
				wantSummary = tc.summary
			}
			assert.Equal(t, wantSummary, coded.FaultSummary())
			assert.EqualError(t, classified, cause.Error())
			assert.ErrorIs(t, classified, cause)
			assert.ErrorIs(t, classified, original)
			assert.Same(t, classified, classifyDockerError(classified))
			var temporary faults.Error
			assert.NotErrorAs(t, classified, &temporary)
		})
	}
}

func TestDockerDiagnosticFaultsAvoidFalsePositives(t *testing.T) {
	for _, diagnostic := range []string{
		"task pending", "waiting for resources", "no eligible nodes", "image pull failed",
		"repository does not exist or may require 'docker login'", "failed to resolve reference: not found",
		"pull access denied for private/app, repository does not exist or may require 'docker login'",
		"pull access denied for busybox, repository does not exist or may require 'docker login'",
		"pull access denied for app",
		"unauthorized", "access denied", "permission denied", "registry request failed: context deadline exceeded",
		"container startup failed", "container exited with code 1", "exec /entrypoint: no such file or directory",
		"failed to create shim task: OCI runtime create failed: executable file not found in $PATH",
		"failed to create endpoint: network not found", "volume data not found",
		"NotFound: lease does not exist: not found", "failed to prepare extraction snapshot: image not found",
	} {
		t.Run(diagnostic, func(t *testing.T) {
			cause := errors.New(diagnostic)
			assert.Same(t, cause, classifyDockerError(cause))
			var coded faults.Coded
			assert.NotErrorAs(t, classifyDockerError(cause), &coded)
		})
	}
	assert.Equal(t, ErrImageNotFound, classifyDockerError(ErrImageNotFound))
}

func TestDockerFaultClassificationUsesOriginalDiagnostic(t *testing.T) {
	for _, context := range []string{
		"no suitable node", "unauthorized: authentication required", "failed to mount volume",
	} {
		t.Run(context, func(t *testing.T) {
			original := errors.New("container startup failed")
			cause := fmt.Errorf("previous failure %q; current request: %w", context, original)
			assert.Same(t, cause, classifyDockerError(cause))
			joined := errors.Join(errors.New(context), original)
			assert.Same(t, joined, classifyDockerError(joined))

			original = errors.New("mount failed: exit status 32")
			cause = fmt.Errorf("previous failure %q; current request: %w", context, original)
			classified := classifyDockerError(cause)
			var coded faults.Coded
			require.ErrorAs(t, classified, &coded)
			assert.Equal(t, faults.CodeMountFailed, coded.FaultCode())
			assert.Equal(t, original.Error(), coded.FaultSummary())
			assert.EqualError(t, classified, cause.Error())
			assert.ErrorIs(t, classified, cause)
			assert.ErrorIs(t, classified, original)

			missingImage := fmt.Errorf("previous failure %q; current request: %w", context, ErrImageNotFound)
			assert.Same(t, missingImage, classifyDockerError(missingImage))
			assert.ErrorIs(t, classifyDockerError(missingImage), ErrImageNotFound)
		})
	}
}

func TestServiceTaskFaults(t *testing.T) {
	for _, tc := range []struct {
		state      TaskState
		diagnostic string
		code       faults.Code
		summary    string
	}{
		{state: TaskStatePending, diagnostic: "no suitable node (insufficient resources on 2 nodes)", code: faults.CodeNoSuitableNode},
		{state: TaskStateRejected, diagnostic: "denied: requested access to the resource is denied", code: faults.CodeImagePullDenied},
		{state: TaskStateRejected, diagnostic: "pull access denied for app", code: ""},
		{state: TaskStateFailed, diagnostic: "VolumeDriver.Mount: backend unavailable", code: faults.CodeMountFailed},
		{state: TaskStateOrphaned, diagnostic: "failed to mount local volume: permission denied", code: faults.CodeMountFailed},
		{state: TaskStateRejected, diagnostic: "No such image: api:2", code: faults.CodeImageNotFound},
		{
			state:      TaskStateRejected,
			diagnostic: `failed to prepare extraction snapshot "extract-225092350-0LGf sha256:95dbfedee91178a3278e2e146a03bfd2223378ee9f60480b71df6f8d6cc1316e": NotFound: lease does not exist: not found`,
			code:       faults.CodeImageExtractionFailed,
			summary:    "Container image extraction failed: containerd snapshot lease is missing. Retry deployment; check for concurrent image cleanup.",
		},
		{state: TaskStateRejected, diagnostic: "docker.io/library/api:2: not found", code: faults.CodeImageNotFound},
		{state: TaskStateRejected, diagnostic: `failed to resolve reference "docker.io/library/api:2": docker.io/library/api:2: not found`, code: faults.CodeImageNotFound},
		{state: TaskStateRejected, diagnostic: "manifest for api:2 not found: manifest unknown: manifest unknown", code: faults.CodeImageNotFound},
		{state: TaskStatePending, diagnostic: "", code: ""},
		{state: TaskStatePending, diagnostic: "waiting for assignment", code: ""},
		{state: TaskStatePending, diagnostic: "unauthorized: authentication required", code: ""},
		{state: TaskStateRunning, diagnostic: "no suitable node", code: ""},
		{state: TaskStateRunning, diagnostic: "VolumeDriver.Mount: failed", code: ""},
		{state: TaskStateRejected, diagnostic: "No such image: different:1", code: ""},
		{state: TaskStateFailed, diagnostic: "No such image: api:2", code: ""},
		{state: TaskStateRejected, diagnostic: "pull failed: context deadline exceeded", code: ""},
		{state: TaskStateRejected, diagnostic: "failed to resolve reference: unexpected status: 500 Internal Server Error", code: ""},
	} {
		t.Run(string(tc.state)+"/"+tc.diagnostic, func(t *testing.T) {
			task := ServiceTask{CurrentState: tc.state, Error: tc.diagnostic,
				Spec: NewTaskSpec(dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "api:2"}})}
			fault := task.Fault()
			if tc.code == "" {
				assert.Nil(t, fault)
				return
			}
			require.NotNil(t, fault)
			assert.Equal(t, tc.code, fault.FaultCode())
			wantSummary := tc.diagnostic
			if tc.summary != "" {
				wantSummary = tc.summary
			}
			assert.Equal(t, wantSummary, fault.FaultSummary())
			assert.EqualError(t, fault, tc.diagnostic)
			assert.Equal(t, tc.code == faults.CodeImageNotFound, task.ImageNotFound())
		})
	}
}
