package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const defaultConfigFixture = `gitops:
  git:
    repository: https://repo-canary.invalid/hidden
  stacks:
    file: stacks.yaml
`

func loadConfigForTool(t *testing.T, custom bool) config.Spec {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm-deploy.yaml")
	content := defaultConfigFixture
	if custom {
		secretPath := filepath.Join(dir, "private-file-canary")
		require.NoError(t, os.WriteFile(secretPath, []byte("secret-content-canary"), 0o600))
		content = fmt.Sprintf(`gitops:
  git:
    repository: https://repo-canary.invalid/hidden
    pull: {interval: 42s}
    auth:
      type: http
      http: {username: git-user-canary, passwordPath: %[1]s}
  sync:
    mode: webhook
    interval: 13s
    policy: {selfHeal: true, prune: true, image: {tag: {required: true, no_latest: true, only_sha: true}}}
    webhook:
      auth: [{type: header, secretPath: %[1]s, header: X-Webhook-Canary}]
  stacks: {file: stacks.yaml}
  secrets: {age: {identityFile: %[1]s}}
deployments: {pollInterval: 4s, initJobs: {maxDuration: 7m}}
event: {history: {capacity: 17}}
system:
  language: ru
  log: {level: DEBUG}
  tracing:
    transport: http
    sampler: {strategy: parentbased_traceidratio, ratio: 0}
    exporter:
      endpoint: https://traces-canary.invalid/v1/traces
      headers: {X-Trace-Canary: exporter-header-canary}
      authentication: {bearerPath: %[1]s}
notifications:
  messengers:
    telegram:
      botTokenPath: %[1]s
      chats:
        default: {id: chat-id-canary, threadId: 92387165}
        chat-alias-canary: {id: second-chat-id-canary}
      retries: 7
      proxy: {socks5: {address: socks-canary.invalid:1234}}
  events:
    DeploySuccess:
      telegram: [{message: notification-template-canary}]
      custom: [{name: route-name-canary, url: https://custom-canary.invalid/hook, header: {X-Custom-Canary: custom-header-canary}}]
    NodeJoined: {}
    SendNotificationFailed:
      telegram: [{message: retired-template-canary}]
  alerts:
    default:
      telegram: [{opened: {message: alert-template-canary}}]
    SyncFailed:
      telegram: [{chats: [default, chat-alias-canary], resolved: {action: edit}}]
web:
  basePath: /swarm/
  pwa: {name: "My Cluster", shortName: "mc"}
  security: {authentication: {authProxy: {loginHeader: X-Login-Canary}}}
healthServer:
  address: health-bind-canary:8081
  healthz: {path: "GET health-host-canary.invalid/live"}
  metrics: {path: /prometheus}
assistant:
  enabled: true
  tools: [date, swarm_deploy_config_get, unknown-tool-canary, date]
  systemPrompt: system-prompt-canary
  model:
    name: model-name-canary
    openai:
      apiTokenPath: %[1]s
      baseUrl: https://provider-canary.invalid/v1
      organizationId: organization-canary
      maxTokens: 1200
  conversation: {storage: {inMemory: {ttl: 30m}}}
hostings: {github: {token: github-token-canary}}
`, secretPath)
	}
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	return cfg.Spec
}

func configToolExecutor(spec config.Spec) *Executor {
	return NewExecutor(&resources.Module{}, &gitops.Module{}, nil, nil, &alertmanagement.Module{},
		&event.Module{}, &swarm.Swarm{}, nil, nil, nil, nil, nil,
		metrics.NewGroup(metrics.CreateGroupParams{}).MCP, spec)
}

func TestSwarmDeployConfigGet(t *testing.T) {
	defaults := configToolExecutor(loadConfigForTool(t, false))
	custom := configToolExecutor(loadConfigForTool(t, true))
	cases := []struct {
		section  string
		defaults string
		custom   string
	}{
		{"", `{
  "gitops":{"syncMode":"hybrid","reconcileInterval":"1m","gitPollingEnabled":true,"gitPollInterval":"30s","webhookEnabled":false,
    "globalPolicy":{"selfHeal":false,"prune":false}},
  "deployments":{"pollInterval":"2s","initJobDefaultTimeout":"10m"},"event":{"historyCapacity":500},
  "system":{"language":"en","logLevel":"INFO","tracing":{"enabled":false}},
  "notifications":{"telegramConfigured":false,"eventTypes":[]},
  "web":{"basePath":"/","authentication":"none","pwa":{"name":"Swarm Deploy","shortName":"Swarm Deploy"}},
  "healthServer":{"healthcheckPath":"/healthz","metricsPath":"/metrics"},
  "assistant":{"enabled":false,"toolAccess":{"mode":"unrestricted"},"maxOutputTokens":800,"conversationInMemoryTTL":"1h"},
  "hostings":{"integrations":[{"type":"github","authMode":"anonymous"}]}
}`, `{
  "gitops":{"syncMode":"webhook","reconcileInterval":"13s","gitPollingEnabled":false,"webhookEnabled":true,
    "globalPolicy":{"selfHeal":true,"prune":true}},
  "deployments":{"pollInterval":"4s","initJobDefaultTimeout":"7m"},"event":{"historyCapacity":17},
  "system":{"language":"ru","logLevel":"DEBUG","tracing":{"enabled":true,"sampler":{"strategy":"parentbased_traceidratio","ratio":0}}},
  "notifications":{"telegramConfigured":true,"telegramMaxAttemptsPerAction":7,"eventTypes":["DeploySuccess"]},
  "web":{"basePath":"/swarm","authentication":"auth_proxy","pwa":{"name":"My Cluster","shortName":"mc"}},
  "healthServer":{"healthcheckPath":"/live","metricsPath":"/prometheus"},
  "assistant":{"enabled":true,"toolAccess":{"mode":"allowlist"},"maxOutputTokens":1200,"conversationInMemoryTTL":"30m"},
  "hostings":{"integrations":[{"type":"github","authMode":"authenticated"}]}
}`},
		{"gitops",
			`{"syncMode":"hybrid","reconcileInterval":"1m","gitPollingEnabled":true,"gitPollInterval":"30s","webhookEnabled":false,
			  "globalPolicy":{"selfHeal":false,"prune":false,"imageTag":{"required":false,"noLatest":false,"onlySHA":false}}}`,
			`{"syncMode":"webhook","reconcileInterval":"13s","gitPollingEnabled":false,"webhookEnabled":true,
			  "globalPolicy":{"selfHeal":true,"prune":true,"imageTag":{"required":true,"noLatest":true,"onlySHA":true}}}`},
		{"deployments", `{"pollInterval":"2s","initJobDefaultTimeout":"10m"}`, `{"pollInterval":"4s","initJobDefaultTimeout":"7m"}`},
		{"event", `{"historyCapacity":500}`, `{"historyCapacity":17}`},
		{"system", `{"language":"en","logLevel":"INFO","tracing":{"enabled":false}}`,
			`{"language":"ru","logLevel":"DEBUG","tracing":{"enabled":true,"sampler":{"strategy":"parentbased_traceidratio","ratio":0}}}`},
		{"web", `{"basePath":"/","authentication":"none","pwa":{"name":"Swarm Deploy","shortName":"Swarm Deploy"}}`,
			`{"basePath":"/swarm","authentication":"auth_proxy","pwa":{"name":"My Cluster","shortName":"mc"}}`},
		{"healthServer", `{"healthcheckPath":"/healthz","metricsPath":"/metrics"}`, `{"healthcheckPath":"/live","metricsPath":"/prometheus"}`},
		{"assistant",
			`{"enabled":false,"toolAccess":{"mode":"unrestricted"},"maxOutputTokens":800,"conversationInMemoryTTL":"1h"}`,
			`{"enabled":true,"toolAccess":{"mode":"allowlist","allowedTools":["date","swarm_deploy_config_get"]},
			  "maxOutputTokens":1200,"conversationInMemoryTTL":"30m"}`},
		{"hostings", `{"integrations":[{"type":"github","authMode":"anonymous"}]}`, `{"integrations":[{"type":"github","authMode":"authenticated"}]}`},
		{"notifications", `{"telegramConfigured":false,"eventTypes":[],"events":[],"alertRouting":[
		  {"kind":"sync_failed","source":"builtInDefault","configuredRecipients":0},
		  {"kind":"node_disconnected","source":"builtInDefault","configuredRecipients":0},
		  {"kind":"git_repository_unavailable","source":"builtInDefault","configuredRecipients":0},
		  {"kind":"service_drift","source":"builtInDefault","configuredRecipients":0},
		  {"kind":"messenger_unavailable","source":"builtInDefault","configuredRecipients":0},
		  {"kind":"messenger_misconfigured","source":"builtInDefault","configuredRecipients":0}
		]}`, `{"telegramConfigured":true,"telegramMaxAttemptsPerAction":7,"eventTypes":["DeploySuccess"],
		"events":[{"type":"DeploySuccess","channels":["telegram","custom"]}],"alertRouting":[
		  {"kind":"sync_failed","source":"kind","configuredRecipients":2,"resolvedActions":["edit"]},
		  {"kind":"node_disconnected","source":"configuredDefault","configuredRecipients":1,"resolvedActions":["reply"]},
		  {"kind":"git_repository_unavailable","source":"configuredDefault","configuredRecipients":1,"resolvedActions":["reply"]},
		  {"kind":"service_drift","source":"configuredDefault","configuredRecipients":1,"resolvedActions":["reply"]},
		  {"kind":"messenger_unavailable","source":"configuredDefault","configuredRecipients":0,"recipientPolicy":"disabledForMessengerUnavailable"},
		  {"kind":"messenger_misconfigured","source":"configuredDefault","configuredRecipients":1,"recipientPolicy":"excludeFailingDestination","resolvedActions":["reply"]}
		]}`},
	}

	for _, tt := range cases {
		name := tt.section
		args, responsePrefix := `{}`, ""
		if tt.section != "" {
			args = fmt.Sprintf(`{"section":%q}`, tt.section)
			responsePrefix = fmt.Sprintf(`"section":%q,`, tt.section)
		} else {
			name = "overview"
		}
		t.Run(name, func(t *testing.T) {
			for _, fixture := range []struct {
				name, want string
				executor   *Executor
			}{{"defaults", tt.defaults, defaults}, {"custom", tt.custom, custom}} {
				t.Run(fixture.name, func(t *testing.T) {
					result, err := fixture.executor.Execute(t.Context(), routing.Request{ToolName: configGetToolName, Payload: args})
					require.NoError(t, err)
					assert.JSONEq(t, `{"scope":"global",`+responsePrefix+`"config":`+fixture.want+"}", result)
					assert.NotContains(t, result, "-canary")
					assert.NotContains(t, result, "92387165")
				})
			}
		})
	}
}

func TestSwarmDeployConfigGetArgumentsAndAllowlist(t *testing.T) {
	spec := loadConfigForTool(t, false)
	executor := configToolExecutor(spec)
	var definition routing.ToolDefinition
	for _, tool := range executor.Definitions() {
		if string(tool.Name) == configGetToolName {
			definition = tool
			break
		}
	}
	require.NotEmpty(t, definition.Name)
	schema, err := json.Marshal(definition.ParametersJSONSchema)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"object","additionalProperties":false,"properties":{"section":{"type":"string",
 "enum":["gitops","deployments","event","system","notifications","web","healthServer","assistant","hostings"]}}}`, string(schema))

	cases := []any{`{"section":"unknown-canary"}`, `{"section":""}`, `{"section":null}`, `{"section":3}`,
		`{"section":"gitops","stack":"unexpected-stack-canary"}`, `{"section":"gitops","section":"gitops"}`,
		`{"Section":"gitops"}`, `{}{}`, `{`, `null`, `[]`, "", nil, 1}
	for _, args := range cases {
		t.Run(fmt.Sprintf("%v", args), func(t *testing.T) {
			result, err := executor.Execute(t.Context(), routing.Request{ToolName: configGetToolName, Payload: args})
			require.ErrorIs(t, err, errConfigGetArguments)
			assert.NotContains(t, err.Error(), "canary")
			assert.Empty(t, result)
		})
	}

	spec.Assistant.Tools = []string{"date"}
	restricted := configToolExecutor(spec)
	result, err := restricted.Execute(t.Context(), routing.Request{ToolName: configGetToolName, Payload: `{}`})
	require.EqualError(t, err, "tool is not allowed by assistant.tools")
	assert.Empty(t, result)
}

func TestSwarmDeployConfigGetTelegramFallback(t *testing.T) {
	spec := loadConfigForTool(t, true)
	spec.Notifications.Alerts = nil
	spec.Notifications.Messengers.Telegram.Retries = loadConfigForTool(t, false).Notifications.Messengers.Telegram.Retries
	view := projectAlertRouting(spec.Notifications, true)
	assert.Equal(t, alertRouteSettings{
		Kind: "sync_failed", Source: "builtInDefault", ConfiguredRecipients: 1, ResolvedActions: []string{"reply"},
	}, view[0])
	result, err := configToolExecutor(spec).Execute(t.Context(), routing.Request{
		ToolName: configGetToolName, Payload: `{"section":"notifications"}`,
	})
	require.NoError(t, err)
	assert.Contains(t, result, `"telegramMaxAttemptsPerAction":3`)
}
