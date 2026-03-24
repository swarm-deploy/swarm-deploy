package rag

import (
	"context"
	"time"

	"github.com/artarts36/swarm-deploy/internal/service"
)

// ServiceStore provides access to current service metadata.
type ServiceStore interface {
	// List returns service metadata rows.
	List() []service.Info
}

// Embedder produces vector embeddings for text inputs.
type Embedder interface {
	// Embed returns vectors for each input item in the same order.
	Embed(ctx context.Context, model string, inputs []string) ([][]float64, error)
}

// Observer records retrieval/indexing telemetry.
type Observer interface {
	// RecordIndexRebuild tracks index rebuild outcome and timing.
	RecordIndexRebuild(status string, size int, duration time.Duration, updatedAt time.Time)
	// RecordRetrieveFallback tracks fallback reasons during retrieval.
	RecordRetrieveFallback(reason string)
}
