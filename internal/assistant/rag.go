package assistant

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/service"
)

type retriever struct {
	store     ServiceStore
	embedder  *openAIClient
	modelName string
}

func newRetriever(store ServiceStore, embedder *openAIClient, modelName string) *retriever {
	return &retriever{
		store:     store,
		embedder:  embedder,
		modelName: strings.TrimSpace(modelName),
	}
}

func (r *retriever) retrieve(ctx context.Context, query string) ([]service.Info, error) {
	services := r.store.List()
	if len(services) == 0 {
		return nil, nil
	}

	documents := make([]string, 0, len(services))
	for _, serviceInfo := range services {
		documents = append(documents, serviceToDocument(serviceInfo))
	}

	inputs := append([]string{query}, documents...)
	embeddings, err := r.embedder.embed(ctx, r.modelName, inputs)
	if err != nil {
		return r.retrieveLexical(query, services), nil
	}
	if len(embeddings) != len(inputs) {
		return nil, fmt.Errorf("invalid embeddings size: got %d, expected %d", len(embeddings), len(inputs))
	}

	queryVector := embeddings[0]
	type scoredService struct {
		service service.Info
		score   float64
	}

	scored := make([]scoredService, 0, len(services))
	for i, serviceInfo := range services {
		score := cosineSimilarity(queryVector, embeddings[i+1])
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

	selected := make([]service.Info, 0)
	for _, item := range scored {
		selected = append(selected, item.service)
	}

	return selected, nil
}

func (r *retriever) retrieveLexical(query string, services []service.Info) []service.Info {
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

	selected := make([]service.Info, 0)
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
	for i := range left {
		dot += left[i] * right[i]
		leftNorm += left[i] * left[i]
		rightNorm += right[i] * right[i]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}

	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}
