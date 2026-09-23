package differ

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	diffmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

func TestDifferCompareComposeServiceLifecycle(t *testing.T) {
	d := New()
	oldCompose := &compose.Compose{Services: compose.Services{
		{Name: "removed"},
		{Name: "same", Image: "app:1"},
	}}
	newCompose := &compose.Compose{Services: compose.Services{
		{Name: "added"},
		{Name: "same", Image: "app:1"},
	}}

	actual := d.CompareCompose("payments", oldCompose, newCompose)

	assert.Equal(t, []diffmodel.ServiceDiff{
		{
			ServiceName: "added", StackName: "payments", Added: true, HasChanges: true,
			Environment: []diffmodel.EnvironmentDiff{}, Networks: []diffmodel.NetworkDiff{},
			Secrets: []diffmodel.SecretDiff{}, Volumes: []diffmodel.VolumeDiff{},
			Configs: []diffmodel.ConfigDiff{}, Ports: []diffmodel.PortDiff{}, InitJobs: []diffmodel.ServiceDiff{},
		},
		{
			ServiceName: "removed", StackName: "payments", Removed: true, HasChanges: true,
			Environment: []diffmodel.EnvironmentDiff{}, Networks: []diffmodel.NetworkDiff{},
			Secrets: []diffmodel.SecretDiff{}, Volumes: []diffmodel.VolumeDiff{},
			Configs: []diffmodel.ConfigDiff{}, Ports: []diffmodel.PortDiff{}, InitJobs: []diffmodel.ServiceDiff{},
		},
	}, actual.Services, "unexpected service lifecycle diff")
}

func TestDifferCompareComposeTopLevelResources(t *testing.T) {
	internal := true
	d := New()
	oldCompose := &compose.Compose{
		Networks: map[string]compose.Network{
			"changed":   {External: false},
			"removed":   {},
			"unchanged": {Internal: &internal},
		},
		Configs: compose.SharedObjects{
			"changed":   {File: "old"},
			"removed":   {},
			"unchanged": {File: "same"},
		},
		Secrets: compose.SharedObjects{
			"changed":   {External: false},
			"removed":   {},
			"unchanged": {Name: "same"},
		},
		Volumes: compose.Volumes{
			"changed":   {Driver: "old"},
			"removed":   {},
			"unchanged": {Name: "same"},
		},
	}
	newCompose := &compose.Compose{
		Networks: map[string]compose.Network{
			"added":     {},
			"changed":   {External: true},
			"unchanged": {Internal: &internal},
		},
		Configs: compose.SharedObjects{
			"added":     {},
			"changed":   {File: "new"},
			"unchanged": {File: "same"},
		},
		Secrets: compose.SharedObjects{
			"added":     {},
			"changed":   {External: true},
			"unchanged": {Name: "same"},
		},
		Volumes: compose.Volumes{
			"added":     {},
			"changed":   {Driver: "new"},
			"unchanged": {Name: "same"},
		},
	}
	expected := []diffmodel.ResourceDiff{
		{StackName: "payments", Name: "added", Added: true},
		{StackName: "payments", Name: "changed", Changed: true},
		{StackName: "payments", Name: "removed", Removed: true},
	}

	actual := d.CompareCompose("payments", oldCompose, newCompose)

	assert.Equal(t, expected, actual.Networks, "unexpected top-level network diff")
	assert.Equal(t, expected, actual.Configs, "unexpected top-level config diff")
	assert.Equal(t, expected, actual.Secrets, "unexpected top-level secret diff")
	assert.Equal(t, expected, actual.Volumes, "unexpected top-level volume diff")
}

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
    volumes:
      - data:/data
      - ./legacy:/legacy
    configs:
      - source: app-config
        target: /etc/app.yaml
      - source: legacy-config
        target: /etc/legacy.yaml
    ports:
      - 80:8080
      - 90:9090
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
    volumes:
      - data:/data:ro
      - ./current:/current
    configs:
      - source: app-config
        target: /etc/app-v2.yaml
      - source: current-config
        target: /etc/current.yaml
    ports:
      - 80:8081
      - 443:8443
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
	assert.Equal(
		t,
		[]diffmodel.VolumeDiff{
			{Type: "bind", Source: "./current", Target: "/current", Added: true},
			{Type: "volume", Source: "data", Target: "/data", ReadOnly: true, Added: true},
			{Type: "volume", Source: "data", Target: "/data", Removed: true},
			{Type: "bind", Source: "./legacy", Target: "/legacy", Removed: true},
		},
		serviceDiff.Volumes,
		"unexpected volume diff",
	)
	assert.Equal(
		t,
		[]diffmodel.ConfigDiff{
			{Name: "app-config", MountFile: "/etc/app-v2.yaml", Added: true},
			{Name: "app-config", MountFile: "/etc/app.yaml", Removed: true},
			{Name: "current-config", MountFile: "/etc/current.yaml", Added: true},
			{Name: "legacy-config", MountFile: "/etc/legacy.yaml", Removed: true},
		},
		serviceDiff.Configs,
		"unexpected config diff",
	)
	assert.Equal(
		t,
		[]diffmodel.PortDiff{
			{Published: 80, Target: 8080, Protocol: "tcp", Mode: "ingress", Removed: true},
			{Published: 80, Target: 8081, Protocol: "tcp", Mode: "ingress", Added: true},
			{Published: 90, Target: 9090, Protocol: "tcp", Mode: "ingress", Removed: true},
			{Published: 443, Target: 8443, Protocol: "tcp", Mode: "ingress", Added: true},
		},
		serviceDiff.Ports,
		"unexpected port diff",
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

func TestDifferCompareInitJobChanges(t *testing.T) {
	testCases := []struct {
		name        string
		oldJobImage string
		newJobImage string
	}{
		{
			name:        "detects changed init job",
			oldJobImage: "migrate:1",
			newJobImage: "migrate:2",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d := New()
			oldCompose := `
services:
  api:
    image: api:1
    x-init-deploy-jobs:
      - name: migrate
        image: ` + testCase.oldJobImage + `
`
			newCompose := `
services:
  api:
    image: api:1
    x-init-deploy-jobs:
      - name: migrate
        image: ` + testCase.newJobImage + `
`

			actual, err := d.Compare([]ComposeFile{
				{
					StackName:      "payments",
					ComposePath:    "payments/docker-compose.yaml",
					OldComposeFile: oldCompose,
					NewComposeFile: newCompose,
				},
			})
			require.NoError(t, err, "compare compose files")
			require.Len(t, actual.Services, 1, "expected parent service change")
			assert.Equal(
				t,
				[]diffmodel.ServiceDiff{
					{
						ServiceName: "migrate",
						HasChanges:  true,
						Image: &diffmodel.ImageDiff{
							Old: testCase.oldJobImage,
							New: testCase.newJobImage,
						},
						Environment: []diffmodel.EnvironmentDiff{},
						Networks:    []diffmodel.NetworkDiff{},
						Secrets:     []diffmodel.SecretDiff{},
						Volumes:     []diffmodel.VolumeDiff{},
						Configs:     []diffmodel.ConfigDiff{},
					},
				},
				actual.Services[0].InitJobs,
				"unexpected init job diff",
			)
		})
	}
}

func TestDifferCompareInitJobCommandChanges(t *testing.T) {
	d := New()
	oldCompose := &compose.Compose{Services: compose.Services{{
		Name: "api",
		InitJobs: []compose.InitJob{{
			Name: "migrate", Image: "migrate:1", Command: []string{"up"},
		}},
	}}}
	newCompose := &compose.Compose{Services: compose.Services{{
		Name: "api",
		InitJobs: []compose.InitJob{{
			Name: "migrate", Image: "migrate:1", Command: []string{"up", "--force"},
		}},
	}}}

	actual := d.CompareCompose("payments", oldCompose, newCompose)

	require.Len(t, actual.Services, 1, "expected parent service change")
	require.Len(t, actual.Services[0].InitJobs, 1, "expected init job change")
	assert.Equal(t, &diffmodel.CommandDiff{
		Old: []string{"up"},
		New: []string{"up", "--force"},
	}, actual.Services[0].InitJobs[0].Command, "unexpected init job command diff")
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
