package rag

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	webroute "github.com/swarm-deploy/webroute/api"
)

type fakeServiceStore struct {
	services []model.Info
}

func (f *fakeServiceStore) List() []model.Info {
	out := make([]model.Info, len(f.services))
	copy(out, f.services)
	return out
}

type fakeEmbedder struct {
	embedFn func(ctx context.Context, model string, inputs []string) ([][]float64, error)
}

func (f *fakeEmbedder) Embed(ctx context.Context, model string, inputs []string) ([][]float64, error) {
	return f.embedFn(ctx, model, inputs)
}

type observerCapture struct {
	reasons []string
}

func (o *observerCapture) RecordIndexRebuild(_ string, _ int, _ time.Duration, _ time.Time) {}

func (o *observerCapture) RecordRetrieveFallback(reason string) {
	o.reasons = append(o.reasons, reason)
}

func runPlan(t *testing.T, retriever *Retriever, query string) []model.Info {
	t.Helper()

	plan, err := retriever.Plan(context.Background(), query)
	require.NoError(t, err, "plan retrieval")

	switch plan.Branch() {
	case RetrievalPlanBranchNone:
		return nil
	case RetrievalPlanBranchLexical:
		selected, lexicalErr := retriever.RetrieveLexical(plan)
		require.NoError(t, lexicalErr, "retrieve lexical")
		return selected
	case RetrievalPlanBranchSemantic:
		selected, semanticErr := retriever.RetrieveSemantic(plan)
		require.NoError(t, semanticErr, "retrieve semantic")
		return selected
	default:
		t.Fatalf("unknown retrieval branch: %s", plan.Branch())
		return nil
	}
}

func TestRetrieverRanksByEmbeddingSimilarity(t *testing.T) {
	services := []model.Info{
		{
			Name:     "api",
			Stack:    "app",
			Metadata: metadata.Metadata{Type: "application"},
			Image:    "example/api:v1",
		},
		{
			Name:     "db",
			Stack:    "app",
			Metadata: metadata.Metadata{Type: "database"},
			Image:    "postgres:16",
		},
		{
			Name:     "worker",
			Stack:    "jobs",
			Metadata: metadata.Metadata{Type: "application"},
			Image:    "example/worker:v1",
		},
	}

	index := NewIndex()
	require.NoError(
		t,
		index.Replace(services, [][]float64{{0.4, 0.1}, {0.9, 0.1}, {0.2, 0.4}}),
		"seed index",
	)

	retriever := NewRetriever(
		&fakeServiceStore{services: services},
		&fakeEmbedder{
			embedFn: func(_ context.Context, _ string, inputs []string) ([][]float64, error) {
				require.Equal(t, []string{"database service"}, inputs, "expected query-only embedding call")
				return [][]float64{{1, 0}}, nil
			},
		},
		"model",
		index,
		nil,
	)

	selected := runPlan(t, retriever, "database service")
	require.Len(t, selected, 3, "expected all services ordered")
	assert.Equal(t, "db", selected[0].Name, "expected nearest service first")
	assert.Equal(t, "api", selected[1].Name, "expected second nearest service")
}

func TestRetrieverFallsBackToLexicalSearchWhenQueryEmbeddingFails(t *testing.T) {
	services := []model.Info{
		{
			Name:     "api",
			Stack:    "app",
			Metadata: metadata.Metadata{Description: "Public API for users"},
		},
		{
			Name:     "queue",
			Stack:    "infra",
			Metadata: metadata.Metadata{Description: "Background jobs queue"},
		},
	}

	index := NewIndex()
	require.NoError(t, index.Replace(services, [][]float64{{0.1, 0.2}, {0.2, 0.1}}), "seed index")

	observer := &observerCapture{}
	retriever := NewRetriever(
		&fakeServiceStore{services: services},
		&fakeEmbedder{
			embedFn: func(_ context.Context, _ string, _ []string) ([][]float64, error) {
				return nil, errors.New("embeddings unavailable")
			},
		},
		"model",
		index,
		observer,
	)

	selected := runPlan(t, retriever, "jobs")
	assert.Equal(t, "queue", selected[0].Name, "expected lexical best match")
	assert.Equal(t, "api", selected[1].Name, "expected second lexical match")
	assert.Equal(t, []string{"query_embedding_error"}, observer.reasons, "expected fallback reason metric")
}

func TestRetrieverLexicalMatchesWebRouteFields(t *testing.T) {
	services := []model.Info{
		{
			Name:  "api",
			Stack: "app",
			WebRoutes: []webroute.WebRoute{
				{
					From: webroute.Address{
						Domain:  "api.example.com",
						Address: "api.example.com/v1",
						Port:    "8080",
					},
				},
			},
		},
		{
			Name:  "queue",
			Stack: "infra",
		},
	}

	index := NewIndex()
	require.NoError(t, index.Replace(services, [][]float64{{0.1, 0.2}, {0.2, 0.1}}), "seed index")

	retriever := NewRetriever(
		&fakeServiceStore{services: services},
		&fakeEmbedder{
			embedFn: func(_ context.Context, _ string, _ []string) ([][]float64, error) {
				return nil, errors.New("embeddings unavailable")
			},
		},
		"model",
		index,
		nil,
	)

	selected := runPlan(t, retriever, "api.example.com")
	assert.Equal(t, "api", selected[0].Name, "expected service match by web route domain")
}

func TestRetrieverLimitsSemanticResultsAndPrioritizesNamedService(t *testing.T) {
	services := []model.Info{
		{Name: "svc-0", Stack: "app"},
		{Name: "svc-1", Stack: "app"},
		{Name: "svc-2", Stack: "app"},
		{Name: "svc-3", Stack: "app"},
		{Name: "svc-4", Stack: "app"},
		{Name: "svc-5", Stack: "app"},
		{Name: "web-gateway-http", Stack: "infra"},
	}
	embeddings := [][]float64{
		{1, 0}, {0.9, 0.1}, {0.8, 0.2}, {0.7, 0.3}, {0.6, 0.4}, {0.5, 0.5}, {0, 1},
	}
	index := NewIndex()
	require.NoError(t, index.Replace(services, embeddings), "seed index")

	retriever := NewRetriever(
		&fakeServiceStore{services: services},
		&fakeEmbedder{embedFn: func(_ context.Context, _ string, _ []string) ([][]float64, error) {
			return [][]float64{{1, 0}}, nil
		}},
		"model",
		index,
		nil,
	)

	selected := runPlan(t, retriever, "проверь DNS для сервиса web-gateway-http")
	require.Len(t, selected, maxRetrievedServices)
	assert.Equal(t, "web-gateway-http", selected[0].Name)
}

func TestRetrieverLimitsLexicalFallbackResults(t *testing.T) {
	services := []model.Info{
		{Name: "svc-0", Stack: "app"},
		{Name: "svc-1", Stack: "app"},
		{Name: "svc-2", Stack: "app"},
		{Name: "svc-3", Stack: "app"},
		{Name: "svc-4", Stack: "app"},
		{Name: "svc-5", Stack: "app"},
		{Name: "web-gateway-http", Stack: "infra"},
	}
	index := NewIndex()
	embeddings := make([][]float64, len(services))
	for idx := range embeddings {
		embeddings[idx] = []float64{1, 0}
	}
	require.NoError(t, index.Replace(services, embeddings), "seed index")

	retriever := NewRetriever(
		&fakeServiceStore{services: services},
		&fakeEmbedder{embedFn: func(_ context.Context, _ string, _ []string) ([][]float64, error) {
			return nil, errors.New("embeddings unavailable")
		}},
		"model",
		index,
		nil,
	)

	selected := runPlan(t, retriever, "web-gateway-http")
	require.Len(t, selected, maxRetrievedServices)
	assert.Equal(t, "web-gateway-http", selected[0].Name)
}
