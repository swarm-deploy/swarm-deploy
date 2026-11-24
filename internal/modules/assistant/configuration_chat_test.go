package assistant

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	configChatPrompt  = "config-system-prompt-canary"
	configChatInvalid = `{"section":"notifications","stack":"invalid-stack-canary"}`
)

func TestConfigurationThroughChat(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	tracing.Enable()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	tests := []struct {
		name, userMessage, arguments, answer, wantResult string
		diagnostic, forbidden, concept, providerError    bool
		allowedTools                                     []string
	}{
		{name: "overview", userMessage: "Что включено в моём swarm-deploy?", arguments: `{}`,
			answer: "Telegram настроен. Self-heal включён глобально.", wantResult: `"historyCapacity":500`},
		{name: "notifications", userMessage: "Как у меня настроены уведомления?", arguments: `{"section":"notifications"}`,
			answer: "Для DeploySuccess настроен Telegram.", wantResult: `"eventTypes":["DeploySuccess"]`},
		{name: "configured tool allowlist", userMessage: "Какие инструменты разрешены?", arguments: `{"section":"assistant"}`,
			allowedTools: []string{configurationToolName}, wantResult: `"allowedTools":["swarm_deploy_config_get"]`,
			answer: "В глобальном списке разрешён инструмент чтения конфигурации. Защитный инструмент доступен отдельно."},
		{name: "init job timeout", userMessage: "Сколько времени отведено init job?", arguments: `{"section":"deployments"}`,
			answer: "По умолчанию на init job отведено 17 минут.", wantResult: `"initJobDefaultTimeout":"17m"`},
		{name: "self-heal diagnosis", userMessage: "Почему у payments не сработал self-heal?", arguments: `{"section":"gitops"}`,
			diagnostic: true, answer: "Глобально self-heal включён. Override стека payments не проверен, причина пока неизвестна.",
			wantResult: `"selfHeal":true`},
		{name: "product concept", userMessage: "Что такое self-heal?", concept: true,
			answer: "Self-heal устраняет расхождение между сервисом и его объявленной конфигурацией."},
		{name: "malformed call", userMessage: "Как у меня настроены уведомления?", arguments: configChatInvalid,
			answer: "Не удалось прочитать настройки уведомлений.", wantResult: "expected a JSON object"},
		{name: "forbidden call", userMessage: "Как у меня настроены уведомления?", arguments: configChatInvalid,
			forbidden: true, allowedTools: []string{"date"},
			answer: "Инструмент чтения конфигурации недоступен.", wantResult: "tool is not allowed"},
		{name: "provider error", userMessage: "Сколько времени отведено init job?", arguments: `{"section":"deployments"}`,
			providerError: true, wantResult: `"initJobDefaultTimeout":"17m"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := configurationChatSpec()
			spec.Assistant.Tools = tt.allowedTools
			route := `{"route":"platform","capabilities":["configuration"],"operation":null}`
			if tt.diagnostic {
				route = `{"route":"diagnostics","capabilities":["configuration","alert_management","pause_sync"],"operation":null}`
			}
			if tt.concept {
				route = `{"route":"platform","capabilities":[],"operation":null}`
			}
			spanOffset := len(recorder.Ended())
			loggerOffset := logs.Len()
			captures := make(chan capturedChatRequest, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				request := decodeCapturedChatRequest(t, req)
				captures <- request
				w.Header().Set("Content-Type", "application/json")
				switch len(captures) {
				case 1:
					writeChatResponse(t, w, route, nil)
				case 2:
					if tt.concept {
						writeChatResponse(t, w, tt.answer, nil)
						return
					}
					calls := []map[string]any{configurationChatCall("call-config", configurationToolName, tt.arguments)}
					if tt.diagnostic {
						calls = append(calls,
							configurationChatCall("call-alert", "alert_list", `{"resource_id":"payments"}`))
					}
					writeChatResponse(t, w, "", calls)
				case 3:
					if tt.providerError {
						w.WriteHeader(http.StatusBadRequest)
						_, err := fmt.Fprint(w, `{"error":{"code":"model_not_found","message":"provider-body-canary"}}`)
						assert.NoError(t, err)
						return
					}
					writeChatResponse(t, w, tt.answer, nil)
				default:
					t.Error("unexpected chat completion")
				}
			}))
			t.Cleanup(server.Close)

			service := newConfigurationChatService(t, server.URL, spec)
			response := service.Chat(t.Context(), ChatRequest{Message: tt.userMessage, WaitTimeoutMS: 5000})
			if tt.providerError {
				require.Equal(t, StatusFailed, response.Status, response)
				assert.Contains(t, logs.String()[loggerOffset:], "HTTP status 400 (model_not_found)")
			} else {
				require.Equal(t, StatusCompleted, response.Status, response)
				assert.Equal(t, tt.answer, response.Answer)
				history, found, err := service.GetChat(t.Context(), response.ConversationID)
				require.NoError(t, err)
				require.True(t, found)
				require.Len(t, history.Messages, 2)
				assert.Equal(t, tt.answer, history.Messages[1].Content)
				assert.NotContains(t, fmt.Sprint(history.Messages[1].Activity), "canary")
			}

			expectedCalls := 3
			if tt.concept {
				expectedCalls = 2
			}
			require.Len(t, captures, expectedCalls)
			router, generation := <-captures, <-captures
			assert.True(t, requestContains(router, tt.userMessage))
			assert.True(t, requestContains(router, "configuration: read actual global"))
			assert.False(t, requestContains(router, configChatPrompt))
			assert.True(t, requestContains(generation, "\n\nProject-specific instructions:\n"+configChatPrompt))
			assert.False(t, requestContains(generation, "initJobDefaultTimeout"))
			assert.Equal(t, !tt.concept && !tt.forbidden,
				slices.Contains(capturedToolNames(generation), configurationToolName))
			if len(tt.allowedTools) != 0 && !tt.forbidden {
				assert.Contains(t, capturedToolNames(generation), assistantPromptInjectionReportTool)
				assert.True(t, requestContains(generation, "the recognized tools in the configured global allowlist"))
				assert.True(t, requestContains(generation, "report tool is available regardless of"))
			}

			if !tt.concept {
				followup := <-captures
				configResult := messageForToolCall(followup, "call-config")
				assert.Contains(t, configResult, tt.wantResult)
				assert.NotContains(t, configResult, "canary")
				assert.NotContains(t, configResult, "99117823")
				assert.NotContains(t, fmt.Sprint(followup.Messages[1:]), "canary")
				if tt.diagnostic {
					assert.Equal(t, `{"alerts":[]}`, messageForToolCall(followup, "call-alert"))
					assert.True(t, requestContains(generation, "has not checked the stack override"))
				}
				wantArguments := tt.arguments
				if wantArguments == configChatInvalid {
					wantArguments = "{}"
				}
				var configCallFound bool
				for _, message := range followup.Messages {
					for _, call := range message.ToolCalls {
						if call.ID == "call-config" {
							configCallFound = true
							assert.Equal(t, wantArguments, call.Function.Arguments)
						}
					}
				}
				assert.True(t, configCallFound)
			}

			var traceText strings.Builder
			for _, span := range recorder.Ended()[spanOffset:] {
				if strings.HasPrefix(span.Name(), "chat.") || strings.HasPrefix(span.Name(), "tool ") {
					fmt.Fprintln(&traceText, span.Name(), span.Attributes(), span.Status(), span.Events())
				}
			}
			traces := traceText.String()
			assert.Contains(t, traces, tt.userMessage)
			assert.NotContains(t, traces, "Project-specific instructions:")
			assert.NotContains(t, traces, "canary")
			assert.NotContains(t, traces, "99117823")
			assert.NotContains(t, logs.String()[loggerOffset:], "canary")
			assert.NotContains(t, fmt.Sprint(response.Activity), "canary")
			if !tt.concept {
				assert.NotContains(t, fmt.Sprint(response.Activity), tt.wantResult)
				assert.Contains(t, traces, "[configuration result omitted]")
				assert.NotContains(t, traces, tt.wantResult)
				if tt.arguments != "{}" {
					assert.NotContains(t, traces, tt.arguments)
					assert.NotContains(t, traces, strings.ReplaceAll(tt.arguments, `"`, `\"`))
				}
			}
			if tt.diagnostic {
				assert.Contains(t, traces, "resource_id")
				assert.Contains(t, traces, "alerts")
			}
		})
	}
}

func newConfigurationChatService(t *testing.T, baseURL string, spec config.Spec) *Service {
	t.Helper()
	db := testutil.OpenSQLite(t)
	executor := tools.NewExecutor(&resources.Module{}, &gitops.Module{}, nil, nil,
		&alertmanagement.Module{Store: alertstore.NewSQLStore(db)}, &event.Module{}, &swarm.Swarm{},
		nil, nil, nil, nil, nil, metrics.NewGroup(metrics.CreateGroupParams{}).MCP, spec)
	service, err := NewService(Config{
		Localizer: testAssistantTexts(t, "en"),
		Enabled:   true, ModelName: "test-model", BaseURL: baseURL, APIToken: "test-token",
		MaxTokens: 200, AllowedTools: spec.Assistant.Tools, SystemPrompt: spec.Assistant.SystemPrompt,
		ConversationInMemoryTTL: time.Hour, ConversationHistory: conversation.NewSQLHistoryStorage(db),
	}, nil, executor, &dispatcher.NopDispatcher{}, metrics.NopAssistant{})
	require.NoError(t, err)
	return service
}

func configurationChatSpec() config.Spec {
	spec := config.Spec{Event: config.EventSpec{History: config.EventHistorySpec{Capacity: 500}}}
	spec.GitOps.Sync = config.SyncSpec{Mode: config.SyncModePull, Policy: config.SyncPolicySpec{SelfHeal: true}}
	spec.GitOps.Git.Repository = "https://repo-canary.invalid/repo"
	spec.GitOps.Git.Auth.HTTP.Password = specw.File{
		Path: "/run/secrets/password-path-canary", Content: []byte("git-password-canary"),
	}
	spec.GitOps.Secrets.Age.IdentityFile.Content = []byte("age-identity-canary")
	spec.Deployments.PollInterval.Value = 3 * time.Second
	spec.Deployments.InitJobs.MaxDuration.Value = 17 * time.Minute
	spec.Notifications.Messengers.Telegram = config.NotificationTelegramSpec{
		BotToken: specw.File{Path: "/run/secrets/bot-path-canary", Content: []byte("telegram-token-canary")},
		Chats:    map[string]config.NotificationChatSpec{"default": {ID: "telegram-chat-canary", ThreadID: 99117823}},
		Retries:  3,
	}
	spec.Notifications.Events = map[events.TypeName]config.NotificationEventRoutes{
		events.TypeNameDeploySuccess: {Telegram: []config.TelegramChannel{{Message: "template-canary"}}},
	}
	spec.System.Tracing = &config.TracingSpec{
		Sampler: config.TracingSamplerSpec{Strategy: specw.Env[string]{Value: config.TracingSamplerParentBasedAlwaysOn}},
		Exporter: config.TracingExporterSpec{
			Endpoint: specw.Env[string]{Value: "https://trace-canary.invalid/"},
			Headers:  map[string]specw.Env[string]{"x-internal-canary": {Value: "trace-header-canary"}},
		},
	}
	spec.Assistant.Enabled = true
	spec.Assistant.SystemPrompt = configChatPrompt
	spec.Assistant.Model.OpenAI.APIToken.Content = []byte("model-token-canary")
	spec.Assistant.Model.OpenAI.OrganizationID = "organization-canary"
	spec.Assistant.Model.OpenAI.BaseURL = "https://provider-canary.invalid/"
	spec.Hostings.GitHub.Token = "github-token-canary"
	return spec
}

func configurationChatCall(id, name, arguments string) map[string]any {
	return map[string]any{
		"id": id, "type": "function", "function": map[string]string{"name": name, "arguments": arguments},
	}
}

func messageForToolCall(request capturedChatRequest, id string) string {
	for _, message := range request.Messages {
		if message.Role == "tool" && message.ToolCallID == id {
			return message.Content
		}
	}
	return ""
}
