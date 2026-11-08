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
	}{
		{"no suitable node (scheduling constraints not satisfied on 3 nodes)", faults.CodeNoSuitableNode},
		{"no suitable node (insufficient resources on 1 node)", faults.CodeNoSuitableNode},
		{"no suitable node (1 node not available for new tasks)", faults.CodeNoSuitableNode},
		{"unauthorized: authentication required", faults.CodeImagePullDenied},
		{"denied: requested access to the resource is denied", faults.CodeImagePullDenied},
		{"no basic auth credentials", faults.CodeImagePullDenied},
		{"insufficient_scope: authorization failed", faults.CodeImagePullDenied},
		{`invalid mount config for type "bind": bind source path does not exist: /srv/data`, faults.CodeMountFailed},
		{"error while creating mount source path '/srv/data': mkdir /srv/data: read-only file system", faults.CodeMountFailed},
		{"error while mounting volume '/var/lib/docker/volumes/data/_data': permission denied", faults.CodeMountFailed},
		{"error while creating volume 'data': VolumeDriver.Create: backend unavailable", faults.CodeMountFailed},
		{"VolumeDriver.Mount: mount operation failed", faults.CodeMountFailed},
		{"failed to mount local volume: mount :/data:/var/lib/docker/volumes/data/_data: permission denied", faults.CodeMountFailed},
		{`error mounting "/srv/data" to rootfs at "/data": permission denied`, faults.CodeMountFailed},
		{"mount failed: exit status 32", faults.CodeMountFailed},
	} {
		t.Run(tc.diagnostic, func(t *testing.T) {
			original := errors.New(tc.diagnostic)
			cause := fmt.Errorf("Error response from daemon: %w", original)
			classified := classifyDockerError(cause)
			var coded faults.Coded
			require.ErrorAs(t, classified, &coded)
			assert.Equal(t, tc.code, coded.FaultCode())
			assert.Equal(t, tc.diagnostic, coded.FaultSummary())
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
	}{
		{TaskStatePending, "no suitable node (insufficient resources on 2 nodes)", faults.CodeNoSuitableNode},
		{TaskStateRejected, "denied: requested access to the resource is denied", faults.CodeImagePullDenied},
		{TaskStateRejected, "pull access denied for app", ""},
		{TaskStateFailed, "VolumeDriver.Mount: backend unavailable", faults.CodeMountFailed},
		{TaskStateOrphaned, "failed to mount local volume: permission denied", faults.CodeMountFailed},
		{TaskStateRejected, "No such image: api:2", faults.CodeImageNotFound},
		{TaskStateRejected, "docker.io/library/api:2: not found", faults.CodeImageNotFound},
		{TaskStateRejected, `failed to resolve reference "docker.io/library/api:2": docker.io/library/api:2: not found`, faults.CodeImageNotFound},
		{TaskStateRejected, "manifest for api:2 not found: manifest unknown: manifest unknown", faults.CodeImageNotFound},
		{TaskStatePending, "", ""},
		{TaskStatePending, "waiting for assignment", ""},
		{TaskStatePending, "unauthorized: authentication required", ""},
		{TaskStateRunning, "no suitable node", ""},
		{TaskStateRunning, "VolumeDriver.Mount: failed", ""},
		{TaskStateRejected, "No such image: different:1", ""},
		{TaskStateFailed, "No such image: api:2", ""},
		{TaskStateRejected, "pull failed: context deadline exceeded", ""},
		{TaskStateRejected, "failed to resolve reference: unexpected status: 500 Internal Server Error", ""},
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
			assert.Equal(t, tc.diagnostic, fault.FaultSummary())
			assert.EqualError(t, fault, tc.diagnostic)
			assert.Equal(t, tc.code == faults.CodeImageNotFound, task.ImageNotFound())
		})
	}
}
