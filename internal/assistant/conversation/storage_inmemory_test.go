package conversation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryStorageAppendAndGet(t *testing.T) {
	storage := NewInMemoryStorage(time.Minute, 3)

	storage.Append("conv-1", Turn{Role: "user", Content: "hello"})
	conversationData, ok := storage.Get("conv-1")
	require.True(t, ok, "expected conversation to exist")
	require.Len(t, conversationData.Turns, 1, "unexpected turn count")
	assert.Equal(t, "user", conversationData.Turns[0].Role, "unexpected role")
	assert.Equal(t, "hello", conversationData.Turns[0].Content, "unexpected content")
	assert.False(t, conversationData.LastMessageAt.IsZero(), "expected last message timestamp")
}

func TestInMemoryStoragePrunesByTTL(t *testing.T) {
	now := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)
	storage := NewInMemoryStorage(10*time.Second, 4)
	storage.now = func() time.Time {
		return now
	}

	storage.Append("conv-1", Turn{Role: "user", Content: "hello"})

	storage.now = func() time.Time {
		return now.Add(11 * time.Second)
	}

	storage.prune()

	_, ok := storage.Get("conv-1")
	assert.False(t, ok, "conversation must be removed after ttl")
}

func TestInMemoryStorageRespectsMaxTurns(t *testing.T) {
	storage := NewInMemoryStorage(time.Minute, 2)
	storage.Append(
		"conv-1",
		Turn{Role: "user", Content: "one"},
		Turn{Role: "assistant", Content: "two"},
		Turn{Role: "user", Content: "three"},
	)

	conversationData, ok := storage.Get("conv-1")
	require.True(t, ok, "expected conversation to exist")
	require.Len(t, conversationData.Turns, 2, "unexpected turn count after trim")
	assert.Equal(t, "two", conversationData.Turns[0].Content, "expected old turns to be trimmed")
	assert.Equal(t, "three", conversationData.Turns[1].Content, "expected latest turn")
}
