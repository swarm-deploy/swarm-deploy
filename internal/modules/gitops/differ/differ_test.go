package differ

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	diffmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

func TestDifferCompareServiceChanges(t *testing.T) {
	d := New()

	oldCompose := `
services:
  api:
    image: ghcr.io/acme/api:1.0.0
    environment:
      A: "1"
      B: "2"
    networks:
      - backend
    secrets:
      - source: app-secret
        target: /run/secrets/app-secret
      - legacy-secret
`

	newCompose := `
services:
  api:
    image: ghcr.io/acme/api:2.0.0
    environment:
      B: "3"
      C: "4"
    networks:
      - frontend
    secrets:
      - source: app-secret
        target: /run/secrets/app-secret-v2
      - current-secret
`

	diff, err := d.Compare([]ComposeFile{
		{
			StackName:      "payments",
			ComposePath:    "payments/docker-compose.yaml",
			OldComposeFile: oldCompose,
			NewComposeFile: newCompose,
		},
	})
	require.NoError(t, err, "compare compose files")
	require.Len(t, diff.Services, 1, "expected one changed service")

	serviceDiff := diff.Services[0]
	assert.Equal(t, "payments", serviceDiff.StackName, "unexpected stack name")
	assert.Equal(t, "api", serviceDiff.ServiceName, "unexpected service name")
	require.NotNil(t, serviceDiff.Image, "expected image diff")
	assert.Equal(t, "ghcr.io/acme/api:1.0.0", serviceDiff.Image.Old, "unexpected old image")
	assert.Equal(t, "ghcr.io/acme/api:2.0.0", serviceDiff.Image.New, "unexpected new image")

	assert.Equal(
		t,
		[]diffmodel.EnvironmentDiff{
			{VarName: "A", Value: "1", Deleted: true},
			{VarName: "B", Value: "3", Changed: true},
			{VarName: "C", Value: "4", Added: true},
		},
		serviceDiff.Environment,
		"unexpected environment diff",
	)
	assert.Equal(
		t,
		[]diffmodel.NetworkDiff{
			{Name: "backend", Connected: false},
			{Name: "frontend", Connected: true},
		},
		serviceDiff.Networks,
		"unexpected network diff",
	)
	assert.Equal(
		t,
		[]diffmodel.SecretDiff{
			{Name: "app-secret", MountFile: "/run/secrets/app-secret", Removed: true},
			{Name: "app-secret", MountFile: "/run/secrets/app-secret-v2", Added: true},
			{Name: "current-secret", MountFile: "/run/secrets/current-secret", Added: true},
			{Name: "legacy-secret", MountFile: "/run/secrets/legacy-secret", Removed: true},
		},
		serviceDiff.Secrets,
		"unexpected secret diff",
	)
}

func TestDifferCompareSkipsUnchangedServices(t *testing.T) {
	d := New()
	composeRaw := `
services:
  api:
    image: ghcr.io/acme/api:1.0.0
`

	diff, err := d.Compare([]ComposeFile{
		{
			StackName:      "payments",
			ComposePath:    "payments/docker-compose.yaml",
			OldComposeFile: composeRaw,
			NewComposeFile: composeRaw,
		},
	})
	require.NoError(t, err, "compare compose files")
	assert.Empty(t, diff.Services, "unchanged compose must not produce service diff")
}

func TestDifferCompareCommitFixingCommandQuoting(t *testing.T) {
	delta, err := New().Compare([]ComposeFile{{
		StackName:      "payments",
		ComposePath:    "payments/docker-compose.yaml",
		OldComposeFile: "services:\n  api:\n    image: alpine:3.21\n    command: echo 'hello\n",
		NewComposeFile: "services:\n  api:\n    image: alpine:3.22\n    command: echo hello\n",
	}})
	require.NoError(t, err)
	require.Len(t, delta.Services, 1)
	require.NotNil(t, delta.Services[0].Image)
	assert.Equal(t, "alpine:3.21", delta.Services[0].Image.Old)
	assert.Equal(t, "alpine:3.22", delta.Services[0].Image.New)
}

func TestDifferCompareFailsOnInvalidCompose(t *testing.T) {
	d := New()

	_, err := d.Compare([]ComposeFile{
		{
			StackName:      "payments",
			ComposePath:    "payments/docker-compose.yaml",
			OldComposeFile: "services:\n  api: [",
			NewComposeFile: "services:\n  api:\n    image: nginx",
		},
	})
	require.Error(t, err, "invalid compose must fail")
	assert.Contains(t, err.Error(), "parse old compose file", "unexpected error")
}
