package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const minimalRuntimeYAML = `gitops:
  git:
    repository: https://example.com/repo.git
  stacks:
    file: stacks.yaml
`

func TestLoadRejectsUnknownRuntimeFields(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		key  string
	}{
		{"unknown root", "unknown: true\n", "unknown"},
		{"legacy git", "git: {}\n", "git"},
		{"legacy swarm", "swarm: {}\n", "swarm"},
		{"removed init jobs timeout", "initJobsTimeout: 10m\n", "initJobsTimeout"},
		{"legacy nested timeout", "gitops: {git: {pullTimeout: 2m}}\n", "pullTimeout"},
		{"legacy poll interval", "gitops: {sync: {pollInterval: 30s}}\n", "pollInterval"},
		{"legacy downward", "gitops: {downward: {enabled: true}}\n", "downward"},
		{"legacy rotation identity", "gitops: {resourceRotation: {age: {}}}\n", "age"},
		{"nested struct", "gitops: {sync: {policy: {image: {tag: {typo: true}}}}}\n", "typo"},
		{"sequence element", "gitops: {sync: {webhook: {auth: [{typo: true}]}}}\n", "typo"},
		{"optional tracing", "system: {tracing: {exporter: {authentication: {typo: x}}}}\n", "typo"},
		{"legacy notifications", "notifications: {on: {DeploySuccess: {}}}\n", "on"},
		{"notification event route", "notifications: {events: {DeploySuccess: {telegram: [{typo: x}]}}}\n", "typo"},
		{"notification chat", "notifications: {messengers: {telegram: {chats: {ops: {typo: x}}}}}\n", "typo"},
		{"second document", minimalRuntimeYAML + "---\nunknown: true\n", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeRuntimeYAML(t, tt.yaml))
			var typeErr *yaml.TypeError
			require.ErrorAs(t, err, &typeErr)
			assert.ErrorContains(t, err, "field "+tt.key+" not found")
		})
	}
}

func TestLoadRejectsMalformedCustomValues(t *testing.T) {
	for _, tt := range []struct {
		name string
		yaml string
	}{
		{"duration mapping", "deployments: {pollInterval: {typo: 2s}}\n"},
		{"file mapping", "gitops: {secrets: {age: {identityFile: {typo: /tmp/identity}}}}\n"},
		{"environment mapping", "system: {tracing: {exporter: {headers: {x-tenant: {typo: x}}}}}\n"},
		{"chat names mapping", "notifications: {events: {DeploySuccess: {telegram: [{chats: {typo: x}}]}}}\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeRuntimeYAML(t, tt.yaml))
			require.ErrorContains(t, err, "decode config yaml")
		})
	}
}

func TestLoadRuntimeModuleDefaults(t *testing.T) {
	cfg, err := Load(writeRuntimeYAML(t, minimalRuntimeYAML))
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.Spec.GitOps.Git.Pull.Interval.Value)
	assert.Equal(t, 2*time.Minute, cfg.Spec.GitOps.Git.Pull.Timeout.Value)
	assert.Equal(t, time.Minute, cfg.Spec.GitOps.Sync.Interval.Value)
	assert.Equal(t, 1, cfg.Spec.GitOps.Sync.Parallelism)
	assert.Equal(t, 2*time.Second, cfg.Spec.Deployments.PollInterval.Value)
	assert.Equal(t, 10*time.Minute, cfg.Spec.Deployments.InitJobs.MaxDuration.Value)
	assert.Equal(t, 500, cfg.Spec.Event.History.Capacity)
	assert.False(t, cfg.Spec.GitOps.Injection.Downward.Enabled)
	assert.Empty(t, cfg.Spec.GitOps.Secrets.Age.IdentityFile.Path)
	assert.Empty(t, cfg.Spec.GitOps.Networks.File)
	assert.Nil(t, cfg.Spec.System.Tracing)
}

func TestLoadRuntimeModuleOverrides(t *testing.T) {
	t.Setenv("SD_TEST_TENANT", "ops")
	cfg, err := Load(writeRuntimeYAML(t, `gitops:
  git:
    repository: https://example.com/repo.git
    pull:
      interval: 12s
  stacks:
    file: stacks.yaml
deployments:
  pollInterval: 250ms
  initJobs:
    maxDuration: 3m
event:
  history:
    capacity: 12
system:
  log:
    level: debug
  tracing:
    transport: grpc
    exporter:
      endpoint: http://otel:4317
      headers:
        x-arbitrary-tenant: $SD_TEST_TENANT
notifications:
  events:
    DeploySuccess:
      custom:
        - name: hook
          url: https://example.com/hook
          method: POST
          header:
            X-Arbitrary-Header: example
`))
	require.NoError(t, err)
	assert.Equal(t, 12*time.Second, cfg.Spec.GitOps.Git.Pull.Interval.Value)
	assert.Equal(t, 250*time.Millisecond, cfg.Spec.Deployments.PollInterval.Value)
	assert.Equal(t, 3*time.Minute, cfg.Spec.Deployments.InitJobs.MaxDuration.Value)
	assert.Equal(t, 12, cfg.Spec.Event.History.Capacity)
	assert.Equal(t, "DEBUG", cfg.Spec.System.Log.Level.String())
	require.NotNil(t, cfg.Spec.System.Tracing)
	assert.Equal(t, "ops", cfg.Spec.System.Tracing.Exporter.Headers["x-arbitrary-tenant"].Value)
	require.Len(t, cfg.Spec.Notifications.Events["DeploySuccess"].Custom, 1)
	assert.Equal(t, "example", cfg.Spec.Notifications.Events["DeploySuccess"].Custom[0].Header["X-Arbitrary-Header"])
}

func TestLoadTelegramButtons(t *testing.T) {
	const routes = `notifications:
  messengers:
    telegram:
      botTokenPath: TOKEN_PATH
      chats:
        default: {id: "-100"}
        ops: {id: "-101"}
  events:
    DeploySuccess:
      telegram:
        - buttons:
            - text: "Open {{ .StackName }}"
              url: "https://example.com/deployments?deployment_id={{ .DeploymentID }}"
  alerts:
    SyncFailed:
      telegram:
        - opened:
            buttons:
              - text: "{{ .Title }}"
                url: "{{ if .DeploymentID }}https://example.com/{{ .DeploymentID }}{{ end }}"
          resolved:
            action: edit
            buttons:
              - text: "{{ .Resolution }}"
                url: "tg://resolve?domain=deployments"
        - chats: ops
          resolved: {buttons: []}
`
	for _, tt := range []struct {
		name, old, replacement, wantError string
	}{
		{name: "valid"},
		{"invalid event URL", "https://example.com/deployments?", "relative?", `events["DeploySuccess"].telegram[0].buttons[0].url`},
		{"invalid opened text", "{{ .Title }}", "{{ .Unknown }}", `.opened.buttons[0].text`},
		{"invalid resolved template", "tg://resolve?domain=deployments", "{{ if }}", `.resolved.buttons[0].url`},
		{"missing label", `text: "{{ .Resolution }}"`, `text: ""`, `.resolved.buttons[0].text`},
		{"callback field", `text: "{{ .Resolution }}"`, `callback_data: "deploy"`, "field callback_data not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tokenPath := filepath.Join(t.TempDir(), "telegram-token")
			require.NoError(t, os.WriteFile(tokenPath, []byte("123:token"), 0o600))
			source := strings.ReplaceAll(routes, "TOKEN_PATH", tokenPath)
			if tt.old != "" {
				source = strings.Replace(source, tt.old, tt.replacement, 1)
			}
			cfg, err := Load(writeRuntimeYAML(t, minimalRuntimeYAML+source))
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			routes := cfg.Spec.Notifications.Alerts["SyncFailed"].Telegram
			assert.Len(t, cfg.Spec.Notifications.Events["DeploySuccess"].Telegram[0].Buttons, 1)
			assert.Len(t, routes[0].Opened.Buttons, 1)
			assert.Len(t, routes[0].Resolved.Buttons, 1)
			assert.NotNil(t, routes[1].Resolved.Buttons)
			assert.Empty(t, routes[1].Resolved.Buttons)
		})
	}
}

func writeRuntimeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "swarm-deploy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
