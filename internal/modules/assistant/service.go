package assistant

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	appconfig "github.com/swarm-deploy/swarm-deploy/internal/config"
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
	defaultPollAfterMS             = 1000
	defaultWaitTimeoutMS           = 12000
	maxWaitTimeoutMS               = 30000
	maxConversationTurns           = 24
	maxAssistantMessageBytes       = 16 * 1024
	maxConcurrentRuns              = 8
	maxRetainedRuns                = 1024
	runRetention                   = 30 * time.Minute
	runExecutionTimeout            = 2 * time.Minute
	conversationPersistenceTimeout = 5 * time.Second
)

const maxRunActivity = 32

var (
	errRequestIDConflict = errors.New("request_id is already bound to another assistant request")
	errConversationBusy  = errors.New("assistant conversation already has an active request")
)

// Service provides assistant chat workflow with start/poll semantics.
type Service struct {
	config Config
	graph  *graph

	ragIndexSubscriber dispatcher.Subscriber

	runsMu   sync.RWMutex
	runs     map[string]*chatRun
	runSlots chan struct{}

	historyMu    sync.Mutex
	historyLocks map[string]chan struct{}

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
	modelClient := newOpenAIClient(config.BaseURL, config.APIToken, config.OrganizationID, config.SystemPrompt)
	embeddingModelName := config.EmbeddingModelName
	if embeddingModelName == "" {
		embeddingModelName = config.ModelName
	}

	ragIndex := rag.NewIndex()
	retriever := rag.NewRetriever(store, modelClient, embeddingModelName, ragIndex, metrics)
	ragSubscriber := rag.NewIndexSubscriber(store, modelClient, embeddingModelName, ragIndex, metrics)
	allowedTools := appconfig.NormalizeAssistantToolAllowlist(config.AllowedTools)

	conversationHistory := config.ConversationHistory

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
		ragIndexSubscriber: ragSubscriber,
		runs:               map[string]*chatRun{},
		runSlots:           make(chan struct{}, maxConcurrentRuns),
		historyLocks:       make(map[string]chan struct{}),
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

// RAGIndexSubscriber returns the subscriber that rebuilds the RAG index from ServiceCatalogUpdated events.
// The module registers it with the Event dispatcher as an external consumer.
func (s *Service) RAGIndexSubscriber() dispatcher.Subscriber {
	return s.ragIndexSubscriber
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
		if conversationID != "" && conversationID != run.conversationID {
			return ChatResponse{
				Status:         StatusFailed,
				ConversationID: conversationID,
				RequestID:      requestID,
				ErrorMessage:   "request_id does not belong to conversation_id",
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
	if len(message) > maxAssistantMessageBytes {
		return ChatResponse{
			Status:         StatusFailed,
			ConversationID: conversationID,
			RequestID:      requestID,
			ErrorMessage:   "message is too large",
		}
	}

	if requestID == "" {
		requestID = uuid.NewString()
	}

	run, created, readErr := s.getOrCreateRun(ctx, requestID, conversationID, message)
	if readErr != nil {
		switch {
		case errors.Is(readErr, errRequestIDConflict):
			return ChatResponse{Status: StatusFailed, ConversationID: conversationID, RequestID: requestID,
				ErrorMessage: readErr.Error()}
		case errors.Is(readErr, errConversationBusy):
			return ChatResponse{Status: StatusFailed, ConversationID: conversationID, RequestID: requestID,
				ErrorMessage: "Assistant is still answering in this chat. Wait for the reply."}
		}
		return ChatResponse{Status: StatusFailed, ConversationID: conversationID, RequestID: requestID,
			ErrorMessage: "Failed to read conversation history"}
	}
	span.SetAttributes(tracing.GenAIConversationID.String(run.conversationID))
	if created {
		s.startRun(ctx, run, request.CreatedBy)
	}

	return s.awaitRun(ctx, run, waitTimeout)
}

// startRun saves the question before generation starts, so the chat survives page reloads and failed replies.
func (s *Service) startRun(ctx context.Context, run *chatRun, createdBy string) {
	select {
	case s.runSlots <- struct{}{}:
	default:
		run.finish(StatusFailed, "", "Assistant is busy. Try again later.")
		return
	}

	history, ok := s.prepareRun(ctx, run, createdBy)
	if !ok {
		<-s.runSlots
		return
	}
	if run.newConversation {
		s.chatObserver.RecordChatCreated()
	}

	go func() {
		defer func() { <-s.runSlots }()
		s.runAssistant(context.WithoutCancel(ctx), createdBy, history, run)
	}()
}

// prepareRun rejects prompt injection, reads model history and saves the question.
// History is read first, so the saved question is passed to the model only once.
func (s *Service) prepareRun(ctx context.Context, run *chatRun, createdBy string) ([]conversation.Turn, bool) {
	if s.graph.guard.Check(run.message) {
		s.publishPromptRejection(ctx, run.message)
		run.reject()
		return nil, false
	}

	history, err := s.getConversation(ctx, run.conversationID)
	if err != nil {
		run.finish(StatusFailed, "", "Failed to read conversation history")
		return nil, false
	}

	unlock, err := s.lockChatHistory(ctx, run.conversationID)
	if err != nil {
		run.finish(StatusFailed, "", "Failed to save assistant message")
		return nil, false
	}
	defer unlock()

	question := conversation.Turn{Role: "user", Content: run.message}
	if err = s.persistConversation(ctx, run.conversationID, createdBy, conversation.TokenUsage{}, question); err != nil {
		run.finish(StatusFailed, "", "Failed to save assistant message")
		return nil, false
	}

	run.markStarted()
	return history, true
}

func (s *Service) runAssistant(ctx context.Context, createdBy string, history []conversation.Turn, run *chatRun) {
	runCtx, cancel := context.WithTimeout(ctx, runExecutionTimeout)
	defer cancel()

	conversationID := run.conversationID
	question := conversation.Turn{Role: "user", Content: run.message}

	run.addActivity("Analyzing request")
	answer, usage, err := s.graph.run(runCtx, conversationID, history, run.message, run.addActivity)
	if err != nil {
		// The saved question stays in history, so the model sees what the user sees.
		s.conversationStorage.Append(conversationID, question)
		if errors.Is(err, guard.ErrPromptInjection) {
			rejectedPrompt := run.message
			var promptErr *guard.PromptInjectionError
			if errors.As(err, &promptErr) && strings.TrimSpace(promptErr.Prompt) != "" {
				rejectedPrompt = promptErr.Prompt
			}

			s.publishPromptRejection(runCtx, rejectedPrompt)
			_ = s.persistConversation(ctx, conversationID, createdBy, usage)
			run.reject()
			return
		}

		_ = s.persistConversation(ctx, conversationID, createdBy, usage)
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

	reply := conversation.Turn{Role: "assistant", Content: answer, Activity: run.activitySnapshot()}

	unlock, err := s.lockChatHistory(ctx, conversationID)
	if err == nil {
		defer unlock()
		err = s.persistConversation(ctx, conversationID, createdBy, usage, reply)
	}
	if err != nil {
		// Keep the model context equal to the saved history: the question stays, the lost reply does not.
		s.conversationStorage.Append(conversationID, question)
		run.finish(StatusFailed, "", "Failed to save assistant response")
		return
	}
	s.conversationStorage.Append(conversationID, question, reply)

	run.finish(StatusCompleted, answer, "")
}

func (s *Service) persistConversation(
	ctx context.Context,
	conversationID string,
	createdBy string,
	usage conversation.TokenUsage,
	turns ...conversation.Turn,
) error {
	if s.conversationHistory == nil || (usage.IsZero() && len(turns) == 0) {
		return nil
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), conversationPersistenceTimeout)
	defer cancel()
	if err := s.conversationHistory.SaveTurns(persistCtx, conversationID, createdBy, usage, turns...); err != nil {
		slog.ErrorContext(
			ctx,
			"[assistant] failed to persist conversation history",
			slog.String("conversation_id", conversationID),
			slog.Any("err", err),
		)
		return err
	}
	return nil
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

func (s *Service) getOrCreateRun(
	ctx context.Context,
	requestID,
	conversationID,
	message string,
) (*chatRun, bool, error) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()

	s.pruneRunsLocked()

	if existing := s.runs[requestID]; existing != nil {
		if (conversationID != "" && existing.conversationID != conversationID) || existing.message != message {
			return nil, false, errRequestIDConflict
		}
		return existing, false, nil
	}
	if conversationID == "" {
		conversationID = uuid.NewString()
	} else if s.activeRunLocked(conversationID) != nil {
		return nil, false, errConversationBusy
	}

	conversationCreated, err := s.isConversationNewLocked(ctx, conversationID)
	if err != nil {
		return nil, false, err
	}

	s.makeRoomForRunLocked()
	run := newChatRun(requestID, conversationID, message, conversationCreated)
	s.runs[requestID] = run
	return run, true, nil
}

// activeRunLocked returns the unfinished run of a conversation. Only one run per conversation can be active.
func (s *Service) activeRunLocked(conversationID string) *chatRun {
	for _, run := range s.runs {
		if run.conversationID == conversationID && !run.isFinished() {
			return run
		}
	}
	return nil
}

// getStartedRun returns the active run whose question is already saved.
func (s *Service) getStartedRun(conversationID string) *chatRun {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()

	run := s.activeRunLocked(conversationID)
	if run == nil || !run.isStarted() {
		return nil
	}
	return run
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

	// Only a started run has saved its question; a failed attempt does not create the conversation.
	for _, run := range s.runs {
		if run.conversationID == conversationID && run.isStarted() {
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

func (s *Service) makeRoomForRunLocked() {
	if len(s.runs) < maxRetainedRuns {
		return
	}

	type finishedRun struct {
		requestID  string
		finishedAt time.Time
	}
	finished := make([]finishedRun, 0, len(s.runs))
	for requestID, run := range s.runs {
		if run.isFinished() {
			finished = append(finished, finishedRun{requestID: requestID, finishedAt: run.finishedAt})
		}
	}
	sort.Slice(finished, func(left, right int) bool {
		return finished[left].finishedAt.Before(finished[right].finishedAt)
	})
	for _, candidate := range finished {
		if len(s.runs) < maxRetainedRuns {
			break
		}
		delete(s.runs, candidate.requestID)
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

// GetUsage returns recorded token consumption across chats.
func (s *Service) GetUsage(ctx context.Context, from, to time.Time, interval UsageInterval) ([]UsageBucket, error) {
	if s.conversationHistory == nil {
		return []UsageBucket{}, nil
	}
	return s.conversationHistory.ReadUsage(ctx, from, to, interval)
}

// lockChatHistory serializes a history snapshot with publishing a saved question or reply
// for the same conversation. Idle chats retain no lock.
func (s *Service) lockChatHistory(ctx context.Context, id string) (func(), error) {
	for {
		s.historyMu.Lock()
		held := s.historyLocks[id]
		if held == nil {
			done := make(chan struct{})
			s.historyLocks[id] = done
			s.historyMu.Unlock()
			return func() {
				s.historyMu.Lock()
				delete(s.historyLocks, id)
				close(done)
				s.historyMu.Unlock()
			}, nil
		}
		s.historyMu.Unlock()
		select {
		case <-held:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// GetChat returns one persisted chat with messages.
func (s *Service) GetChat(ctx context.Context, id string) (ChatHistory, bool, error) {
	if s.conversationHistory == nil {
		return ChatHistory{}, false, nil
	}

	unlock, err := s.lockChatHistory(ctx, id)
	if err != nil {
		return ChatHistory{}, false, err
	}
	defer unlock()

	chat, ok, err := s.conversationHistory.ReadChat(ctx, id)
	if err != nil || !ok {
		return ChatHistory{}, ok, err
	}
	active := s.getStartedRun(id)

	messages := make([]ChatMessage, 0, len(chat.Turns))
	for _, turn := range chat.Turns {
		messages = append(messages, ChatMessage{
			Role: turn.Role, Content: turn.Content, Activity: append([]string(nil), turn.Activity...),
		})
	}

	history := ChatHistory{
		ID: chat.ID, Title: chat.Title, CreatedAt: chat.CreatedAt, UpdatedAt: chat.UpdatedAt,
		Usage: chat.Usage, Messages: messages,
	}
	if active != nil {
		history.ActiveRequest = &ActiveChatRequest{RequestID: active.requestID, Activity: active.activitySnapshot()}
	}
	return history, true, nil
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

type chatRun struct {
	requestID       string
	conversationID  string
	message         string
	newConversation bool

	done chan struct{}

	mu         sync.RWMutex
	status     Status
	started    bool
	answer     string
	error      string
	finishedAt time.Time
	activity   []string
}

func newChatRun(requestID, conversationID, message string, newConversation bool) *chatRun {
	return &chatRun{
		requestID:       requestID,
		conversationID:  conversationID,
		message:         message,
		newConversation: newConversation,
		done:            make(chan struct{}),
		status:          StatusInProgress,
	}
}

// markStarted records that the question was accepted and saved, so a new conversation ID can be exposed.
func (r *chatRun) markStarted() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.started = true
}

func (r *chatRun) isStarted() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.started
}

func (r *chatRun) reject() {
	r.finish(StatusRejected, "", "Request was rejected by prompt injection protection. Rephrase your debugging question.")
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

	conversationID := r.conversationID
	if r.newConversation && !r.started {
		// The conversation was never saved, so clients must not open it.
		conversationID = ""
	}
	return ChatResponse{
		Status:         r.status,
		ConversationID: conversationID,
		RequestID:      r.requestID,
		Answer:         r.answer,
		ErrorMessage:   r.error,
		Activity:       append([]string(nil), r.activity...),
	}
}
func (s *Service) publishPromptRejection(ctx context.Context, prompt string) {
	err := s.event.Publish(ctx, &events.AssistantPromptInjectionDetected{
		Prompt: strings.TrimSpace(prompt), Detector: events.AssistantPromptInjectionDetectorRegexp,
	})
	if err != nil {
		slog.ErrorContext(ctx, "persist prompt rejection event", "err", err)
	}
}
