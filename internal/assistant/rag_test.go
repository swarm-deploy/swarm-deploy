package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeServiceStore struct {
	services []service.Info
}

func (f *fakeServiceStore) List() []service.Info {
	out := make([]service.Info, len(f.services))
	copy(out, f.services)
	return out
}

func TestRetrieverRanksByEmbeddingSimilarity(t *testing.T) {
	services := []service.Info{
		{Name: "api", Stack: "app", Type: "application", Image: "example/api:v1"},
		{Name: "db", Stack: "app", Type: "database", Image: "postgres:16"},
		{Name: "worker", Stack: "jobs", Type: "application", Image: "example/worker:v1"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 0, "embedding": []float64{1, 0}},
				{"index": 1, "embedding": []float64{0.4, 0.1}},
				{"index": 2, "embedding": []float64{0.9, 0.1}},
				{"index": 3, "embedding": []float64{0.2, 0.4}},
			},
		})
	}))
	defer server.Close()

	client := newOpenAIClient(server.URL, "test-token")
	retriever := newRetriever(&fakeServiceStore{services: services}, client, "model")
	selected, err := retriever.retrieve(context.Background(), "database service", 2)
	require.NoError(t, err, "retrieve services")
	require.Len(t, selected, 2, "expected top-k services")
	assert.Equal(t, "db", selected[0].Name, "expected nearest service first")
	assert.Equal(t, "api", selected[1].Name, "expected second nearest service")
}

func TestRetrieverFallsBackToLexicalSearch(t *testing.T) {
	services := []service.Info{
		{Name: "api", Stack: "app", Description: "Public API for users"},
		{Name: "queue", Stack: "infra", Description: "Background jobs queue"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "embeddings unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()

	retriever := newRetriever(
		&fakeServiceStore{services: services},
		newOpenAIClient(server.URL, "test-token"),
		"model",
	)

	selected, err := retriever.retrieve(context.Background(), "jobs", 1)
	require.NoError(t, err, "lexical fallback must still succeed")
	require.Len(t, selected, 1, "expected single service")
	assert.Equal(t, "queue", selected[0].Name, "expected lexical best match")
}
