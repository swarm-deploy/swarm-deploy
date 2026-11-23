package rag

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
)

// ServiceStore provides access to current service metadata.
type ServiceStore interface {
	// List returns service metadata rows.
	ReadAll(context.Context) ([]model.Info, error)
}

// Embedder produces vector embeddings for text inputs.
type Embedder interface {
	// Embed returns ordered vectors and any usage reported by the provider, including on error.
	Embed(ctx context.Context, model string, inputs []string) ([][]float64, conversation.TokenUsage, error)
}

// Observer records retrieval/indexing telemetry.
type Observer interface {
	// RecordIndexRebuild tracks index rebuild outcome and timing.
	RecordIndexRebuild(status string, size int, duration time.Duration, updatedAt time.Time)
	// RecordRetrieveFallback tracks fallback reasons during retrieval.
	RecordRetrieveFallback(reason string)
}
