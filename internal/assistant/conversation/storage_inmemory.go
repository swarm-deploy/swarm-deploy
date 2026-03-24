package conversation

import (
	"log/slog"
	"sync"
	"time"
)

// InMemoryStorage stores conversations in memory and removes expired ones by TTL.
type InMemoryStorage struct {
	mu            sync.Mutex
	ttl           time.Duration
	maxTurns      int
	conversations map[string]Conversation
	now           func() time.Time
}

// NewInMemoryStorage creates in-memory conversation storage with ttl pruning.
func NewInMemoryStorage(ttl time.Duration, maxTurns int) *InMemoryStorage {
	s := &InMemoryStorage{
		ttl:           ttl,
		maxTurns:      maxTurns,
		conversations: map[string]Conversation{},
		now:           time.Now,
	}

	go s.pruneBackground()

	return s
}

// Get returns conversation by id and a flag indicating whether it exists.
func (s *InMemoryStorage) Get(conversationID string) (Conversation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	conversationData, ok := s.conversations[conversationID]
	if !ok {
		return Conversation{}, false
	}

	return copyConversation(conversationData), true
}

// Append appends turns to a conversation and updates last message timestamp.
func (s *InMemoryStorage) Append(conversationID string, turns ...Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	conversationData := s.conversations[conversationID]
	conversationData.Turns = append(conversationData.Turns, turns...)
	if s.maxTurns > 0 && len(conversationData.Turns) > s.maxTurns {
		conversationData.Turns = conversationData.Turns[len(conversationData.Turns)-s.maxTurns:]
	}
	conversationData.LastMessageAt = s.now()

	s.conversations[conversationID] = conversationData
}

func (s *InMemoryStorage) pruneBackground() {
	tc := time.Tick(time.Minute)

	for range tc {
		s.prune()
	}
}

func (s *InMemoryStorage) prune() {
	deleted := 0
	cutoff := s.now().Add(-s.ttl)

	s.mu.Lock()
	defer s.mu.Unlock()

	for conversationID, conversationData := range s.conversations {
		if conversationData.LastMessageAt.IsZero() || conversationData.LastMessageAt.Before(cutoff) {
			delete(s.conversations, conversationID)
			deleted++
		}
	}

	slog.Info("[assistant][conversation-inmemory] pruned", slog.Int("deleted", deleted))
}

func copyConversation(conversationData Conversation) Conversation {
	out := conversationData
	out.Turns = make([]Turn, len(conversationData.Turns))
	copy(out.Turns, conversationData.Turns)
	return out
}
