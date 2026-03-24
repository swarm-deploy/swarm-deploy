package rag

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/service"
)

// Retriever returns services relevant to a user query.
type Retriever struct {
	store     ServiceStore
	embedder  Embedder
	modelName string
	index     *Index
	observer  Observer
}

// NewRetriever creates retriever with precomputed-document index support.
func NewRetriever(store ServiceStore, embedder Embedder, modelName string, index *Index, observer Observer) *Retriever {
	return &Retriever{
		store:     store,
		embedder:  embedder,
		modelName: strings.TrimSpace(modelName),
		index:     index,
		observer:  observer,
	}
}

// Retrieve returns services ordered by relevance.
func (r *Retriever) Retrieve(ctx context.Context, query string) ([]service.Info, error) {
	services := r.store.List()
	if len(services) == 0 {
		return nil, nil
	}

	indexed := r.index.get()
	if !sameServices(indexed.services, services) {
		r.recordFallback("index_stale")
		slog.DebugContext(ctx, "[assistant-rag] fallback to lexical: stale index snapshot")
		return r.retrieveLexical(query, services), nil
	}

	if len(indexed.services) == 0 || len(indexed.embeddings) == 0 {
		r.recordFallback("index_empty")
		slog.DebugContext(ctx, "[assistant-rag] fallback to lexical: empty index")
		return r.retrieveLexical(query, services), nil
	}

	queryEmbeddings, err := r.embedder.Embed(ctx, r.modelName, []string{query})
	if err != nil {
		r.recordFallback("query_embedding_error")
		slog.WarnContext(ctx, "[assistant-rag] fallback to lexical: query embedding failed", slog.Any("err", err))
		return r.retrieveLexical(query, services), nil
	}
	if len(queryEmbeddings) != 1 {
		return nil, fmt.Errorf("invalid query embeddings size: got %d, expected 1", len(queryEmbeddings))
	}

	queryVector := queryEmbeddings[0]
	type scoredService struct {
		service service.Info
		score   float64
	}

	scored := make([]scoredService, 0, len(indexed.services))
	for idx, serviceInfo := range indexed.services {
		score := cosineSimilarity(queryVector, indexed.embeddings[idx])
		scored = append(scored, scoredService{
			service: serviceInfo,
			score:   score,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].service.Stack != scored[j].service.Stack {
			return scored[i].service.Stack < scored[j].service.Stack
		}
		return scored[i].service.Name < scored[j].service.Name
	})
	selected := make([]service.Info, 0, len(scored))
	for _, item := range scored {
		selected = append(selected, item.service)
	}

	return selected, nil
}

func (r *Retriever) retrieveLexical(query string, services []service.Info) []service.Info {
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	if normalizedQuery == "" {
		return services
	}

	terms := strings.Fields(normalizedQuery)
	type scoredService struct {
		service service.Info
		score   int
	}
	scored := make([]scoredService, 0, len(services))

	for _, serviceInfo := range services {
		doc := strings.ToLower(serviceToDocument(serviceInfo))
		score := 0
		for _, term := range terms {
			if strings.Contains(doc, term) {
				score++
			}
		}
		scored = append(scored, scoredService{
			service: serviceInfo,
			score:   score,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].service.Stack != scored[j].service.Stack {
			return scored[i].service.Stack < scored[j].service.Stack
		}
		return scored[i].service.Name < scored[j].service.Name
	})

	selected := make([]service.Info, 0, len(scored))
	for _, item := range scored {
		selected = append(selected, item.service)
	}

	return selected
}

func serviceToDocument(serviceInfo service.Info) string {
	return strings.TrimSpace(
		fmt.Sprintf(
			"stack=%s service=%s type=%s image=%s description=%s",
			serviceInfo.Stack,
			serviceInfo.Name,
			serviceInfo.Type,
			serviceInfo.Image,
			serviceInfo.Description,
		),
	)
}

func cosineSimilarity(left, right []float64) float64 {
	if len(left) == 0 || len(right) == 0 || len(left) != len(right) {
		return 0
	}

	var dot float64
	var leftNorm float64
	var rightNorm float64
	for idx := range left {
		dot += left[idx] * right[idx]
		leftNorm += left[idx] * left[idx]
		rightNorm += right[idx] * right[idx]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}

	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func sameServices(left, right []service.Info) bool {
	if len(left) != len(right) {
		return false
	}

	for idx := range left {
		if left[idx].Stack != right[idx].Stack ||
			left[idx].Name != right[idx].Name ||
			left[idx].Type != right[idx].Type ||
			left[idx].Image != right[idx].Image ||
			left[idx].Description != right[idx].Description {
			return false
		}
	}

	return true
}

func (r *Retriever) recordFallback(reason string) {
	if r.observer == nil {
		return
	}

	r.observer.RecordRetrieveFallback(reason)
}
