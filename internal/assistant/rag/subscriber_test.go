package rag

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/webroute"
)

type countingEmbedder struct {
	inputs [][]string
	result [][]float64
}

func (c *countingEmbedder) Embed(_ context.Context, _ string, inputs []string) ([][]float64, error) {
	copied := make([]string, len(inputs))
	copy(copied, inputs)
	c.inputs = append(c.inputs, copied)
	return c.result, nil
}

type subscriberObserverCapture struct {
	rebuildStatuses []string
}

func (o *subscriberObserverCapture) RecordIndexRebuild(
	status string,
	_ int,
	_ time.Duration,
	_ time.Time,
) {
	o.rebuildStatuses = append(o.rebuildStatuses, status)
}

func (*subscriberObserverCapture) RecordRetrieveFallback(string) {}

func TestIndexSubscriberBuildsIndexOnDeploySuccess(t *testing.T) {
	services := []service.Info{
		{
			Name:  "api",
			Stack: "app",
			Type:  "application",
			Image: "example/api:v1",
			WebRoutes: []webroute.Route{
				{
					Domain:  "api.example.com",
					Address: "api.example.com/v1",
					Port:    "8080",
				},
			},
		},
		{Name: "db", Stack: "app", Type: "database", Image: "postgres:16"},
	}
	store := &fakeServiceStore{services: services}
	embedder := &countingEmbedder{
		result: [][]float64{{0.2, 0.1}, {0.8, 0.1}},
	}

	index := NewIndex()
	observer := &subscriberObserverCapture{}
	subscriber := NewIndexSubscriber(store, embedder, "model", index, observer)

	err := subscriber.Handle(context.Background(), &events.DeploySuccess{
		StackName: "app",
		Commit:    "abc",
	})
	require.NoError(t, err, "handle deploySuccess")
	assert.Equal(t, []string{"success"}, observer.rebuildStatuses, "expected rebuild metric")
	require.Len(t, embedder.inputs, 1, "expected index embeddings build")
	assert.Len(t, embedder.inputs[0], 2, "expected one embedding input per service")
	assert.True(
		t,
		strings.Contains(embedder.inputs[0][0], "web_route_address=api.example.com/v1"),
		"expected web route fields in embedding document",
	)

	retriever := NewRetriever(
		store,
		&fakeEmbedder{
			embedFn: func(_ context.Context, _ string, inputs []string) ([][]float64, error) {
				require.Equal(t, []string{"database"}, inputs, "expected query-only embedding")
				return [][]float64{{1, 0}}, nil
			},
		},
		"model",
		index,
		nil,
	)

	selected := runPlan(t, retriever, "database")
	assert.Equal(t, "db", selected[0].Name, "expected nearest by precomputed index")
}
