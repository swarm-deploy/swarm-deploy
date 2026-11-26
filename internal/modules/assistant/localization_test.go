package assistant

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/guard"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"go.uber.org/mock/gomock"
)

func TestAssistantSendsInstallationLanguageToProviderForExistingChat(t *testing.T) {
	for _, tc := range []struct {
		locale, question, oldAnswer, instruction, answer, activity string
	}{
		{"en", "Как установить Prometheus?", "Старый ответ", "Reply in English.", "Add Prometheus to Compose.", "Generating response"},
		{"ru", "How do I install Prometheus?", "Previous answer", "Reply in Russian.", "Добавьте Prometheus в Compose.", "Формирование ответа"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			history := newSQLHistory(t)
			oldQuestion := conversation.Turn{Role: "user", Content: "Prometheus"}
			oldReply := conversation.Turn{Role: "assistant", Content: tc.oldAnswer, Activity: []string{"Original activity"}}
			_, err := history.SaveTurns(t.Context(), "existing", conversation.SaveOptions{}, conversation.TokenUsage{}, oldQuestion, oldReply)
			require.NoError(t, err)

			var requests []capturedChatRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, decodeCapturedChatRequest(t, req))
				w.Header().Set("Content-Type", "application/json")
				answer := tc.answer
				if len(requests) == 1 {
					answer = `{"route":"platform","capabilities":[],"operation":null}`
				}
				writeChatResponse(t, w, answer, nil)
			}))
			t.Cleanup(server.Close)

			tools := NewMockToolExecutor(gomock.NewController(t))
			tools.EXPECT().Definitions().Return([]routing.ToolDefinition{}).AnyTimes()
			service := newRoutingTestServiceWithLocale(t, server.URL, serviceStore(t, nil), tools, nil, history, tc.locale)
			response := service.Chat(t.Context(), ChatRequest{ConversationID: "existing", Message: tc.question})
			require.Equal(t, StatusCompleted, response.Status, response.ErrorMessage)
			assert.Equal(t, tc.answer, response.Answer)
			assert.Contains(t, response.Activity, tc.activity)

			require.Len(t, requests, 2)
			for _, request := range requests {
				assert.Equal(t, tc.question, request.Messages[len(request.Messages)-1].Content)
				assert.True(t, requestContains(request, tc.oldAnswer))
			}
			assert.NotContains(t, requests[0].Messages[0].Content, `"language"`)
			assert.Contains(t, requests[1].Messages[0].Content, tc.instruction)
			assert.NotContains(t, requests[1].Messages[0].Content, "language of the user's latest message")

			chat, exists, err := service.GetChat(t.Context(), "existing")
			require.NoError(t, err)
			require.True(t, exists)
			require.Len(t, chat.Messages, 4)
			assert.Equal(t, oldReply.Content, chat.Messages[1].Content)
			assert.Equal(t, oldReply.Activity, chat.Messages[1].Activity)
			assert.Equal(t, tc.question, chat.Messages[2].Content)
		})
	}
}

func TestAssistantLocalGreetingAndValidationUseInstallationLanguage(t *testing.T) {
	for _, tc := range []struct{ locale, greeting, reply, validation string }{
		{"en", "привет", "Hello! How can I help with swarm-deploy?", "A message is required for a new assistant request."},
		{"ru", "hello", "Привет! Чем помочь со swarm-deploy?", "Для нового запроса к ассистенту нужно сообщение."},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("greeting called the provider")
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			service := newRoutingTestServiceWithLocale(t, server.URL, serviceStore(t, nil), nil, nil, nil, tc.locale)
			reply := service.Chat(t.Context(), ChatRequest{Message: tc.greeting})
			assert.Equal(t, StatusCompleted, reply.Status)
			assert.Equal(t, tc.reply, reply.Answer)

			invalid := service.Chat(t.Context(), ChatRequest{})
			assert.Equal(t, StatusFailed, invalid.Status)
			assert.Equal(t, tc.validation, invalid.ErrorMessage)
		})
	}
}

func TestAssistantConfirmsAndCancelsInBothLanguages(t *testing.T) {
	for _, tc := range []struct{ locale, yes, no, question, confirmed, cancelled, yesLabel, noLabel string }{
		{"en", "да", "нет", "Start GitOps synchronization?", "Synchronization request queued successfully.", "Operation cancelled.", "Yes", "No"},
		{"ru", "yes", "no", "Запустить синхронизацию GitOps?", "Запрос на синхронизацию добавлен в очередь.", "Операция отменена.", "Да", "Нет"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			tools := NewMockToolExecutor(gomock.NewController(t))
			tools.EXPECT().Execute(gomock.Any(), routing.Request{ToolName: deploySyncTriggerTool, Payload: "{}"}).
				Return(`{"queued":true}`, nil)
			operation := newOperationFlow(Config{Localizer: testAssistantTexts(t, tc.locale)}, nil, nil, tools, guard.NewInjectionChecker(), nil)

			for _, reply := range []struct{ input, answer string }{{tc.yes, tc.confirmed}, {tc.no, tc.cancelled}} {
				turn := operation.beginTurn("chat")
				question, _ := turn.startPendingOperation(t.Context(), OperationIntent{Type: OperationDeploySync})
				assert.Equal(t, tc.question, question)
				require.NotNil(t, turn.selection)
				assert.Equal(t, []conversation.SelectionOption{
					{Label: tc.yesLabel, Value: tc.yesLabel},
					{Label: tc.noLabel, Value: tc.noLabel},
				}, turn.selection.Options)
				turn.publish()

				handled, answer, _, err := operation.beginTurn("chat").handlePendingOperation(t.Context(), reply.input)
				require.NoError(t, err)
				assert.True(t, handled)
				assert.Equal(t, reply.answer, answer)
			}
		})
	}
}
