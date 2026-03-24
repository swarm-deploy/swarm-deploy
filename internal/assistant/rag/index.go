package rag

import (
	"fmt"
	"sync"
	"time"

	"github.com/artarts36/swarm-deploy/internal/service"
)

type snapshot struct {
	services   []service.Info
	embeddings [][]float64
	updatedAt  time.Time
}

// Index stores precomputed document embeddings for retrieval.
type Index struct {
	mu       sync.RWMutex
	snapshot snapshot
}

// NewIndex creates an empty embeddings index.
func NewIndex() *Index {
	return &Index{}
}

// Replace atomically replaces index data.
func (i *Index) Replace(services []service.Info, embeddings [][]float64) error {
	if len(services) != len(embeddings) {
		return fmt.Errorf("invalid embeddings size: got %d, expected %d", len(embeddings), len(services))
	}

	nextServices := make([]service.Info, len(services))
	copy(nextServices, services)

	nextEmbeddings := make([][]float64, len(embeddings))
	for idx, vector := range embeddings {
		if len(vector) == 0 {
			return fmt.Errorf("empty embedding at index %d", idx)
		}
		nextVector := make([]float64, len(vector))
		copy(nextVector, vector)
		nextEmbeddings[idx] = nextVector
	}

	i.mu.Lock()
	i.snapshot = snapshot{
		services:   nextServices,
		embeddings: nextEmbeddings,
		updatedAt:  time.Now(),
	}
	i.mu.Unlock()

	return nil
}

// Clear removes indexed data.
func (i *Index) Clear() {
	i.mu.Lock()
	i.snapshot = snapshot{}
	i.mu.Unlock()
}

func (i *Index) get() snapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()

	copied := snapshot{
		services:   make([]service.Info, len(i.snapshot.services)),
		embeddings: make([][]float64, len(i.snapshot.embeddings)),
		updatedAt:  i.snapshot.updatedAt,
	}
	copy(copied.services, i.snapshot.services)
	for idx, vector := range i.snapshot.embeddings {
		nextVector := make([]float64, len(vector))
		copy(nextVector, vector)
		copied.embeddings[idx] = nextVector
	}

	return copied
}
