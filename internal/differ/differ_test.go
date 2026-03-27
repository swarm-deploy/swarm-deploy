package differ

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		[]EnvironmentDiff{
			{VarName: "A", Value: "1", Deleted: true},
			{VarName: "B", Value: "3", Changed: true},
			{VarName: "C", Value: "4", Added: true},
		},
		serviceDiff.Environment,
		"unexpected environment diff",
	)
	assert.Equal(
		t,
		[]NetworkDiff{
			{Name: "backend", Connected: false},
			{Name: "frontend", Connected: true},
		},
		serviceDiff.Networks,
		"unexpected network diff",
	)
	assert.Equal(
		t,
		[]SecretDiff{
			{Name: "app-secret", MountFile: "/run/secrets/app-secret", Removed: true},
			{Name: "app-secret", MountFile: "/run/secrets/app-secret-v2", Added: true},
			{Name: "current-secret", Added: true},
			{Name: "legacy-secret", Removed: true},
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
