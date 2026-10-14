package assistant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"go.uber.org/mock/gomock"
)

func TestAssistantToolExecutionRounds(t *testing.T) {
	testCases := []struct {
		name      string
		toolNames []string
	}{
		{name: "one round followed by final response", toolNames: []string{"alert_list"}},
		{name: "three rounds followed by final response", toolNames: []string{
			"alert_list", "deployment_list", "deployment_get",
		}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			controller := gomock.NewController(t)
			toolExecutor := NewMockToolExecutor(controller)
			toolExecutor.EXPECT().Definitions().Return(toolChainDefinitions()).AnyTimes()
			var previousCall *gomock.Call
			for index, toolName := range testCase.toolNames {
				round := strconv.Itoa(index + 1)
				request := routing.Request{ToolName: toolName, Payload: `{"round":` + round + `}`}
				executeCall := toolExecutor.EXPECT().Execute(gomock.Any(), request).
					Return(`{"round":`+round+`}`, nil)
				if previousCall != nil {
					executeCall.After(previousCall)
				}
				previousCall = executeCall
			}

			var requests []capturedChatRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, decodeCapturedChatRequest(t, req))
				w.Header().Set("Content-Type", "application/json")
				call := len(requests)
				switch {
				case call == 1:
					writeChatResponse(t, w, `{"route":"diagnostics","capabilities":["deployment_history","alert_management"],"operation":null}`, nil)
				case call <= len(testCase.toolNames)+1:
					index := call - 2
					round := strconv.Itoa(index + 1)
					writeChatResponse(t, w, "", []map[string]any{{
						"id": "tool-" + round, "type": "function",
						"function": map[string]any{
							"name": testCase.toolNames[index], "arguments": `{"round":` + round + `}`,
						},
					}})
				default:
					writeChatResponse(t, w, "final answer", nil)
				}
			}))
			defer server.Close()

			assistantService := newRoutingTestService(t, server.URL, serviceStore(t, nil), toolExecutor, nil)
			response := assistantService.Chat(context.Background(), ChatRequest{Message: "Investigate the deployment"})

			require.Equal(t, StatusCompleted, response.Status)
			assert.Equal(t, "final answer", response.Answer)
			require.Len(t, requests, len(testCase.toolNames)+2)
			finalRequest := requests[len(requests)-1]
			if len(testCase.toolNames) == maxToolIterations {
				assert.Empty(t, finalRequest.Tools)
				assert.True(t, requestContains(finalRequest, toolLimitFinalAnswer))
			} else {
				assert.NotEmpty(t, finalRequest.Tools)
				assert.False(t, requestContains(finalRequest, toolLimitFinalAnswer))
			}
		})
	}
}

func TestAssistantToolExecutionLimitDoesNotExecuteFourthRound(t *testing.T) {
	controller := gomock.NewController(t)
	toolExecutor := NewMockToolExecutor(controller)
	toolExecutor.EXPECT().Definitions().Return(toolChainDefinitions()).AnyTimes()
	gomock.InOrder(
		toolExecutor.EXPECT().Execute(gomock.Any(), routing.Request{ToolName: "alert_list", Payload: `{}`}).Return(`{"step":1}`, nil),
		toolExecutor.EXPECT().Execute(gomock.Any(), routing.Request{ToolName: "deployment_list", Payload: `{}`}).Return(`{"step":2}`, nil),
		toolExecutor.EXPECT().Execute(gomock.Any(), routing.Request{ToolName: "deployment_get", Payload: `{}`}).Return(`{"step":3}`, nil),
	)

	toolNames := []string{"alert_list", "deployment_list", "deployment_get"}
	var requests []capturedChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, decodeCapturedChatRequest(t, req))
		w.Header().Set("Content-Type", "application/json")
		switch len(requests) {
		case 1:
			writeChatResponse(t, w, `{"route":"diagnostics","capabilities":["deployment_history","alert_management"],"operation":null}`, nil)
		case 2, 3, 4:
			name := toolNames[len(requests)-2]
			writeChatResponse(t, w, "", []map[string]any{modelFunctionToolCall("tool-limit", name)})
		default:
			writeChatResponse(t, w, "", []map[string]any{modelFunctionToolCall("forbidden-fourth", "alert_get")})
		}
	}))
	defer server.Close()

	assistantService := newRoutingTestService(t, server.URL, serviceStore(t, nil), toolExecutor, nil)
	response := assistantService.Chat(context.Background(), ChatRequest{Message: "Investigate the deployment"})

	require.Equal(t, StatusCompleted, response.Status)
	assert.Equal(t, toolLimitFallbackAnswer, response.Answer)
	require.Len(t, requests, 5, "router, three tool-generating completions, and one bounded finalization are expected")
	assert.Empty(t, requests[4].Tools)
	assert.True(t, requestContains(requests[4], toolLimitFinalAnswer))
}

func TestAssistantAlertDeploymentToolChain(t *testing.T) {
	const alertResult = `{"alerts":[{"id":"alert-7","status":"open","kind":"sync_failed","resource_type":"stack","resource_id":"infra-monitoring","title":"Synchronization failed","updated_at":"2026-10-11T12:00:00Z","deployment_id":"deploy-42","error_code":"apply_failed"}]}`
	const deploymentResult = `{"deployment":{"id":"deploy-42","stack":"infra-monitoring","status":"failed","phase":"apply","apply_status":"failed","error_code":"apply_failed","error_diagnostic":"network monitor-overlay not found"}}`

	controller := gomock.NewController(t)
	toolExecutor := NewMockToolExecutor(controller)
	toolExecutor.EXPECT().Definitions().Return(toolChainDefinitions()).AnyTimes()
	gomock.InOrder(
		toolExecutor.EXPECT().Execute(gomock.Any(), routing.Request{
			ToolName: "alert_list",
			Payload:  `{"status":"open","kind":"sync_failed","resource_type":"stack","resource_id":"infra-monitoring","limit":5}`,
		}).Return(alertResult, nil),
		toolExecutor.EXPECT().Execute(gomock.Any(), routing.Request{
			ToolName: "deployment_get",
			Payload:  `{"id":"deploy-42"}`,
		}).Return(deploymentResult, nil),
	)

	var requests []capturedChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, decodeCapturedChatRequest(t, req))
		w.Header().Set("Content-Type", "application/json")
		switch len(requests) {
		case 1:
			writeChatResponse(t, w, `{"route":"diagnostics","capabilities":["service_context","deployment_history","alert_management"],"operation":null}`, nil)
		case 2:
			writeChatResponse(t, w, "", []map[string]any{{
				"id": "alert-list-1", "type": "function", "function": map[string]any{
					"name":      "alert_list",
					"arguments": `{"status":"open","kind":"sync_failed","resource_type":"stack","resource_id":"infra-monitoring","limit":5}`,
				},
			}})
		case 3:
			writeChatResponse(t, w, "", []map[string]any{{
				"id": "deployment-get-1", "type": "function", "function": map[string]any{
					"name": "deployment_get", "arguments": `{"id":"deploy-42"}`,
				},
			}})
		default:
			writeChatResponse(t, w, "Деплой infra-monitoring упал на этапе apply: сеть monitor-overlay не найдена.", nil)
		}
	}))
	defer server.Close()

	assistantService := newRoutingTestService(t, server.URL, serviceStore(t, []model.Info{
		{Name: "monitor", Stack: "infra-monitoring"},
	}), toolExecutor, nil)
	response := assistantService.Chat(context.Background(), ChatRequest{Message: "Почему упал деплой infra-monitoring?"})

	require.Equal(t, StatusCompleted, response.Status)
	assert.Contains(t, response.Answer, "monitor-overlay")
	require.Len(t, requests, 4)
	assert.True(t, requestContains(requests[2], alertResult), "alert result must reach the deployment selection completion")
	assert.True(t, requestContains(requests[3], deploymentResult), "deployment diagnostics must reach final generation")
	assert.False(t, requestCallsTool(requests, "alert_get"), "compact alert correlation must not require alert_get")
}

func TestAssistantAlertWithoutDeploymentDoesNotInventAttempt(t *testing.T) {
	const alertResult = `{"alerts":[{"id":"alert-prep","status":"open","kind":"sync_failed","resource_type":"stack","resource_id":"infra-monitoring","title":"Preparation failed","updated_at":"2026-10-11T12:00:00Z","error_code":"prepare_failed"}]}`

	controller := gomock.NewController(t)
	toolExecutor := NewMockToolExecutor(controller)
	toolExecutor.EXPECT().Definitions().Return(toolChainDefinitions()).AnyTimes()
	toolExecutor.EXPECT().Execute(gomock.Any(), routing.Request{
		ToolName: "alert_list",
		Payload:  `{"status":"open","resource_id":"infra-monitoring"}`,
	}).Return(alertResult, nil)

	var requests []capturedChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, decodeCapturedChatRequest(t, req))
		w.Header().Set("Content-Type", "application/json")
		switch len(requests) {
		case 1:
			writeChatResponse(t, w, `{"route":"diagnostics","capabilities":["deployment_history","alert_management"],"operation":null}`, nil)
		case 2:
			writeChatResponse(t, w, "", []map[string]any{{
				"id": "alert-list-1", "type": "function", "function": map[string]any{
					"name": "alert_list", "arguments": `{"status":"open","resource_id":"infra-monitoring"}`,
				},
			}})
		default:
			writeChatResponse(t, w, "Сбой произошёл при подготовке; deployment_id отсутствует, поэтому попытка деплоя не создавалась.", nil)
		}
	}))
	defer server.Close()

	assistantService := newRoutingTestService(t, server.URL, serviceStore(t, nil), toolExecutor, nil)
	response := assistantService.Chat(context.Background(), ChatRequest{Message: "Почему упал деплой infra-monitoring?"})

	require.Equal(t, StatusCompleted, response.Status)
	assert.Contains(t, response.Answer, "попытка деплоя не создавалась")
	require.Len(t, requests, 3)
	assert.False(t, requestCallsTool(requests, "deployment_get"))
	assert.True(t, requestContains(requests[2], alertResult))
}

func toolChainDefinitions() []routing.ToolDefinition {
	names := []string{"alert_list", "alert_get", "deployment_list", "deployment_get"}
	definitions := make([]routing.ToolDefinition, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, routing.ToolDefinition{
			Name: config.AssistantToolName(name), ParametersJSONSchema: map[string]any{"type": "object"},
		})
	}
	return definitions
}

func requestCallsTool(requests []capturedChatRequest, toolName string) bool {
	for _, request := range requests {
		for _, message := range request.Messages {
			for _, call := range message.ToolCalls {
				if call.Function.Name == toolName {
					return true
				}
			}
		}
	}
	return false
}
