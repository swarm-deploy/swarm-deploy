package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/guard"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/rag"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultPollAfterMS   = 1000
	defaultWaitTimeoutMS = 12000
	maxWaitTimeoutMS     = 30000
	maxConversationTurns = 24
	runRetention         = 30 * time.Minute
	runExecutionTimeout  = 2 * time.Minute
)

const maxRunActivity = 32

// Service provides assistant chat workflow with start/poll semantics.
type Service struct {
	config Config
	graph  *graph

	runsMu sync.RWMutex
	runs   map[string]*chatRun

	conversationStorage conversation.Storage
	conversationHistory conversation.HistoryStorage

	event        dispatcher.Dispatcher
	chatObserver metrics.Assistant

	tracer trace.Tracer
}

// RAGObserver records RAG indexing and retrieval telemetry.
type RAGObserver = rag.Observer

// NewService creates assistant service with OpenAI-compatible clients.
func NewService(
	config Config,
	store ServiceStore,
	tools ToolExecutor,
	eventDispatcher dispatcher.Dispatcher,
	metrics metrics.Assistant,
) (*Service, error) {
	modelClient := newOpenAIClient(config.BaseURL, config.APIToken, config.OrganizationID)
	embeddingModelName := config.EmbeddingModelName
	if embeddingModelName == "" {
		embeddingModelName = config.ModelName
	}

	ragIndex := rag.NewIndex()
	retriever := rag.NewRetriever(store, modelClient, embeddingModelName, ragIndex, metrics)
	ragSubscriber := rag.NewIndexSubscriber(store, modelClient, embeddingModelName, ragIndex, metrics)
	allowedTools := normalizeAllowedTools(config.AllowedTools)
	eventDispatcher.Subscribe(events.TypeDeploySuccess, ragSubscriber)

	conversationHistory := config.ConversationHistory
	if conversationHistory == nil && strings.TrimSpace(config.ConversationHistoryDir) != "" {
		history, err := conversation.NewFileHistoryStorage(config.ConversationHistoryDir)
		if err != nil {
			return nil, fmt.Errorf("create assistant conversation history: %w", err)
		}
		conversationHistory = history
	}

	return &Service{
		config: config,
		graph: newGraph(
			config,
			guard.NewInjectionChecker(),
			retriever,
			modelClient,
			newLLMRouter(modelClient, config.ModelName),
			tools,
			allowedTools,
			metrics,
			store,
		),
		runs: map[string]*chatRun{},
		conversationStorage: conversation.NewInMemoryStorage(
			config.ConversationInMemoryTTL,
			maxConversationTurns,
		),
		conversationHistory: conversationHistory,
		event:               eventDispatcher,
		chatObserver:        metrics,
		tracer:              otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"),
	}, nil
}

// Chat handles new assistant requests and polling requests.
func (s *Service) Chat(ctx context.Context, request ChatRequest) ChatResponse {
	ctx, span := s.tracer.Start(ctx, "assistant.Chat")
	defer span.End()

	conversationID := strings.TrimSpace(request.ConversationID)
	requestID := strings.TrimSpace(request.RequestID)
	message := strings.TrimSpace(request.Message)

	if conversationID != "" {
		span.SetAttributes(tracing.GenAIConversationID.String(conversationID))
	}

	waitTimeout := sanitizeWaitTimeout(request.WaitTimeoutMS)

	if requestID != "" && message == "" {
		run := s.getRun(requestID)
		if run == nil {
			return ChatResponse{
				Status:         StatusFailed,
				ConversationID: conversationID,
				RequestID:      requestID,
				ErrorMessage:   "unknown request_id",
			}
		}

		return s.awaitRun(ctx, run, waitTimeout)
	}

	if message == "" {
		return ChatResponse{
			Status:         StatusFailed,
			ConversationID: conversationID,
			RequestID:      requestID,
			ErrorMessage:   "message is required for new assistant request",
		}
	}

	if conversationID == "" {
		conversationID = uuid.NewString()
		span.SetAttributes(tracing.GenAIConversationID.String(conversationID))
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}

	run, created, conversationCreated, readErr := s.getOrCreateRun(ctx, requestID, conversationID)
	if readErr != nil {
		return ChatResponse{Status: StatusFailed, ConversationID: conversationID, RequestID: requestID,
			ErrorMessage: "Failed to read conversation history"}
	}
	if created {
		if conversationCreated {
			s.chatObserver.RecordChatCreated()
		}
		go s.runAssistant(context.WithoutCancel(ctx), conversationID, message, run)
	}

	return s.awaitRun(ctx, run, waitTimeout)
}

func (s *Service) runAssistant(
	ctx context.Context,
	conversationID,
	message string,
	run *chatRun,
) {
	runCtx, cancel := context.WithTimeout(ctx, runExecutionTimeout)
	defer cancel()

	run.addActivity("Analyzing request")
	history, err := s.getConversation(runCtx, conversationID)
	if err != nil {
		run.finish(StatusFailed, "", "Failed to read conversation history")
		return
	}
	answer, usage, err := s.graph.run(runCtx, conversationID, history, message, run.addActivity)
	if err != nil {
		if errors.Is(err, errPromptInjection) {
			rejectedPrompt := message
			var promptErr *promptInjectionError
			if errors.As(err, &promptErr) && strings.TrimSpace(promptErr.prompt) != "" {
				rejectedPrompt = promptErr.prompt
			}

			run.finish(
				StatusRejected,
				"",
				"Request was rejected by prompt injection protection. Rephrase your debugging question.",
			)

			s.event.Dispatch(runCtx, &events.AssistantPromptInjectionDetected{
				Prompt:   strings.TrimSpace(rejectedPrompt),
				Detector: events.AssistantPromptInjectionDetectorRegexp,
			})

			return
		}

		slog.ErrorContext(
			runCtx,
			"[assistant] failed to generate response",
			slog.String("conversation_id", conversationID),
			slog.String("request_id", run.requestID),
			slog.Any("err", err),
		)
		run.finish(StatusFailed, "", "Failed to generate assistant response")
		return
	}

	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = "No answer generated by model."
	}

	turns := []conversation.Turn{
		{Role: "user", Content: message},
		{Role: "assistant", Content: answer, Activity: run.activitySnapshot()},
	}

	if s.conversationHistory != nil {
		if err = s.conversationHistory.SaveTurns(runCtx, conversationID, usage, turns...); err != nil {
			slog.ErrorContext(
				runCtx,
				"[assistant] failed to persist conversation history",
				slog.String("conversation_id", conversationID),
				slog.Any("err", err),
			)
			run.finish(StatusFailed, "", "Failed to persist conversation history")
			return
		}
	}
	s.conversationStorage.Append(conversationID, turns...)

	run.finish(StatusCompleted, answer, "")
}

func (s *Service) awaitRun(ctx context.Context, run *chatRun, waitTimeout time.Duration) ChatResponse {
	select {
	case <-run.done:
		return run.snapshot()
	default:
	}

	timer := time.NewTimer(waitTimeout)
	defer timer.Stop()

	select {
	case <-run.done:
		return run.snapshot()
	case <-timer.C:
		response := run.snapshot()
		response.PollAfterMS = defaultPollAfterMS
		return response
	case <-ctx.Done():
		response := run.snapshot()
		response.PollAfterMS = defaultPollAfterMS
		return response
	}
}

func (s *Service) getRun(requestID string) *chatRun {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()

	return s.runs[requestID]
}

func (s *Service) getOrCreateRun(ctx context.Context, requestID, conversationID string) (*chatRun, bool, bool, error) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()

	s.pruneRunsLocked()

	if existing := s.runs[requestID]; existing != nil {
		return existing, false, false, nil
	}

	conversationCreated, err := s.isConversationNewLocked(ctx, conversationID)
	if err != nil {
		return nil, false, false, err
	}

	run := newChatRun(requestID, conversationID)
	s.runs[requestID] = run
	return run, true, conversationCreated, nil
}

func (s *Service) isConversationNewLocked(ctx context.Context, conversationID string) (bool, error) {
	if _, ok := s.conversationStorage.Get(conversationID); ok {
		return false, nil
	}
	if s.conversationHistory != nil {
		_, ok, err := s.conversationHistory.ReadChat(ctx, conversationID)
		if err != nil {
			return false, err
		}
		if ok {
			return false, nil
		}
	}

	for _, run := range s.runs {
		if run.conversationID == conversationID {
			return false, nil
		}
	}

	return true, nil
}

func (s *Service) pruneRunsLocked() {
	cutoff := time.Now().Add(-runRetention)
	for requestID, run := range s.runs {
		if !run.isFinished() {
			continue
		}
		if run.finishedAt.Before(cutoff) {
			delete(s.runs, requestID)
		}
	}
}

func (s *Service) getConversation(ctx context.Context, conversationID string) ([]conversation.Turn, error) {
	conversationData, ok := s.conversationStorage.Get(conversationID)
	if ok {
		return conversationData.Turns, nil
	}
	if s.conversationHistory == nil {
		return nil, nil
	}

	persisted, ok, err := s.conversationHistory.ReadChat(ctx, conversationID)
	if err != nil || !ok {
		return nil, err
	}

	s.conversationStorage.Append(conversationID, persisted.Turns...)
	conversationData, ok = s.conversationStorage.Get(conversationID)
	if !ok {
		return nil, nil
	}

	return conversationData.Turns, nil
}

// ListChats returns persisted chats ordered by latest activity.
func (s *Service) ListChats(ctx context.Context) ([]ChatSummary, error) {
	if s.conversationHistory == nil {
		return []ChatSummary{}, nil
	}

	chats, err := s.conversationHistory.ReadChats(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]ChatSummary, 0, len(chats))
	for _, chat := range chats {
		result = append(result, ChatSummary{
			ID: chat.ID, Title: chat.Title, CreatedAt: chat.CreatedAt, UpdatedAt: chat.UpdatedAt, Usage: chat.Usage,
		})
	}
	return result, nil
}

// GetChat returns one persisted chat with messages.
func (s *Service) GetChat(ctx context.Context, id string) (ChatHistory, bool, error) {
	if s.conversationHistory == nil {
		return ChatHistory{}, false, nil
	}

	chat, ok, err := s.conversationHistory.ReadChat(ctx, id)
	if err != nil || !ok {
		return ChatHistory{}, ok, err
	}

	messages := make([]ChatMessage, 0, len(chat.Turns))
	for _, turn := range chat.Turns {
		messages = append(messages, ChatMessage{
			Role: turn.Role, Content: turn.Content, Activity: append([]string(nil), turn.Activity...),
		})
	}

	return ChatHistory{
		ID: chat.ID, Title: chat.Title, CreatedAt: chat.CreatedAt, UpdatedAt: chat.UpdatedAt,
		Usage: chat.Usage, Messages: messages,
	}, true, nil
}

func sanitizeWaitTimeout(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		timeoutMS = defaultWaitTimeoutMS
	}
	if timeoutMS > maxWaitTimeoutMS {
		timeoutMS = maxWaitTimeoutMS
	}

	return time.Duration(timeoutMS) * time.Millisecond
}

func normalizeAllowedTools(allowedTools []string) map[string]struct{} {
	normalized := map[string]struct{}{}
	for _, toolName := range allowedTools {
		toolName = strings.TrimSpace(toolName)
		if toolName == "" {
			continue
		}
		normalized[toolName] = struct{}{}
	}

	return normalized
}

type chatRun struct {
	requestID      string
	conversationID string

	done chan struct{}

	mu         sync.RWMutex
	status     Status
	answer     string
	error      string
	finishedAt time.Time
	activity   []string
}

func newChatRun(requestID, conversationID string) *chatRun {
	return &chatRun{
		requestID:      requestID,
		conversationID: conversationID,
		done:           make(chan struct{}),
		status:         StatusInProgress,
	}
}

func (r *chatRun) addActivity(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.activity) > 0 && r.activity[len(r.activity)-1] == message {
		return
	}
	if len(r.activity) >= maxRunActivity {
		copy(r.activity, r.activity[1:])
		r.activity[len(r.activity)-1] = message
	} else {
		r.activity = append(r.activity, message)
	}
}

func (r *chatRun) activitySnapshot() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return append([]string(nil), r.activity...)
}

func (r *chatRun) isFinished() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.status != StatusInProgress
}

func (r *chatRun) finish(status Status, answer string, errorMessage string) {
	r.mu.Lock()
	if r.status != StatusInProgress {
		r.mu.Unlock()
		return
	}

	r.status = status
	r.answer = strings.TrimSpace(answer)
	r.error = strings.TrimSpace(errorMessage)
	r.finishedAt = time.Now()
	r.mu.Unlock()

	close(r.done)
}

func (r *chatRun) snapshot() ChatResponse {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return ChatResponse{
		Status:         r.status,
		ConversationID: r.conversationID,
		RequestID:      r.requestID,
		Answer:         r.answer,
		ErrorMessage:   r.error,
		Activity:       append([]string(nil), r.activity...),
	}
}
