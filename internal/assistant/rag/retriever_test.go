package rag

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/webroute"
)

type fakeServiceStore struct {
	services []service.Info
}

func (f *fakeServiceStore) List() []service.Info {
	out := make([]service.Info, len(f.services))
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

func runPlan(t *testing.T, retriever *Retriever, query string) []service.Info {
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
	services := []service.Info{
		{Name: "api", Stack: "app", Type: "application", Image: "example/api:v1"},
		{Name: "db", Stack: "app", Type: "database", Image: "postgres:16"},
		{Name: "worker", Stack: "jobs", Type: "application", Image: "example/worker:v1"},
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
	services := []service.Info{
		{Name: "api", Stack: "app", Description: "Public API for users"},
		{Name: "queue", Stack: "infra", Description: "Background jobs queue"},
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
	services := []service.Info{
		{
			Name:  "api",
			Stack: "app",
			WebRoutes: []webroute.Route{
				{
					Domain:  "api.example.com",
					Address: "api.example.com/v1",
					Port:    "8080",
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
