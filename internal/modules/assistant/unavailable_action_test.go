package assistant

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
)

func TestChatExplainsUnavailableActionsBeforeCollectingTargets(t *testing.T) {
	for _, tc := range []struct {
		name, message, decision, limitation string
		allow                               []string
		missingTool                         string
		readTool                            string
	}{
		{"create", "Поставь prometheus в docker swarm", `{"route":"services","operation":{"type":"unsupported"}}`, "no matching executable operation", nil, "", ""},
		{"change image", "Обнови образ core/api", `{"route":"services","capabilities":[],"operation":{"type":"unsupported"}}`, "no matching executable operation", nil, "", ""},
		{"unrecognized write", "Создай сервис", `{"route":"services","operation":{"type":"service_create"}}`, "no matching executable operation", nil, "", ""},
		{"disabled", "Перезапусти сервис", `{"route":"services","operation":{"type":"service_restart_trigger"}}`, "assistant.tools setting disables service_restart_trigger", []string{"date"}, "", ""},
		{"unregistered", "Синхронизируй", `{"route":"deployments","operation":{"type":"deploy_sync_trigger"}}`, "unavailable in this installation", nil, "deploy_sync_trigger", ""},
		{"compound", "Поставь паузу и три реплики core/api", `{"route":"services","operation":{"type":"sync_pause_set","scope":"service","target":"core/api","replicas":3}}`, "assistant.tools setting disables service_replicas_set", []string{"sync_pause_set"}, "", ""},
		{"independent read", "Создай сервис и покажи открытые алерты", `{"route":"diagnostics","capabilities":["alert_management"],"operation":{"type":"unsupported"}}`, "no matching executable operation", nil, "", "alert_list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, reads := observedServiceStore(t, []model.Info{{Stack: "core", Name: "prometheus"}})
			defs := slices.DeleteFunc(assistantTestToolDefinitions(), func(d routing.ToolDefinition) bool {
				return string(d.Name) == tc.missingTool
			})
			tools := &fakeTools{definitions: defs}
			var requests []capturedChatRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, decodeCapturedChatRequest(t, req))
				w.Header().Set("Content-Type", "application/json")
				if len(requests) == 1 {
					writeChatResponse(t, w, tc.decision, nil)
					return
				}
				writeChatResponse(t, w, "Инструкция по изменению.", nil)
			}))
			defer server.Close()
			svc := newRoutingTestService(t, server.URL, store, tools, tc.allow)
			answer := svc.Chat(t.Context(), ChatRequest{Message: tc.message})
			require.Equal(t, StatusCompleted, answer.Status)
			assert.Equal(t, "Инструкция по изменению.", answer.Answer)
			require.Len(t, requests, 2)
			assert.Contains(t, requests[1].Messages[0].Content, tc.limitation)
			if tc.readTool != "" {
				assert.Contains(t, capturedToolNames(requests[1]), tc.readTool)
			}
			_, pending := svc.graph.pending.get(answer.ConversationID)
			assert.False(t, pending)
			assert.Zero(t, reads.Load())
			assert.Empty(t, tools.calls)
		})
	}
}
