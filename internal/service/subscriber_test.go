package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	serviceDescription "github.com/artarts36/swarm-deploy/internal/service/description"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type labelsInspectorMock struct {
	labelsByService map[string]swarm.ServiceLabels
	errByService    map[string]error
}

func (m *labelsInspectorMock) InspectServiceLabels(
	_ context.Context,
	stackName, serviceName, _ string,
) (swarm.ServiceLabels, error) {
	key := stackName + "/" + serviceName
	return m.labelsByService[key], m.errByService[key]
}

func TestSubscriberHandleDeploySuccessPersistsServices(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "services.json"))
	require.NoError(t, err, "new store")

	inspector := &labelsInspectorMock{
		labelsByService: map[string]swarm.ServiceLabels{
			"app/web": {
				Container: map[string]string{
					serviceDescription.LabelService: "Public web gateway",
				},
				Service: map[string]string{
					labelServiceType: "delivery",
				},
			},
		},
	}
	subscriber := NewSubscriber(
		store,
		inspector,
		NewResolver(map[string]Type{"postgres": TypeDatabase}),
	)

	err = subscriber.Handle(context.Background(), &events.DeploySuccess{
		StackName: "app",
		Services: []compose.Service{
			{
				Name:  "db",
				Image: "docker.io/library/postgres:16",
			},
			{
				Name:  "web",
				Image: "ghcr.io/acme/web:1.2.3",
			},
		},
	})
	require.NoError(t, err, "handle deploy success")

	assert.Equal(
		t,
		[]Info{
			{
				Name:  "db",
				Stack: "app",
				Type:  TypeDatabase,
				Image: "docker.io/library/postgres:16",
			},
			{
				Name:        "web",
				Stack:       "app",
				Description: "Public web gateway",
				Type:        TypeDelivery,
				Image:       "ghcr.io/acme/web:1.2.3",
			},
		},
		store.List(),
		"expected persisted stack snapshot",
	)
}

func TestSubscriberHandleUsesLabelsEvenWhenInspectorReturnsError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "services.json"))
	require.NoError(t, err, "new store")

	inspector := &labelsInspectorMock{
		labelsByService: map[string]swarm.ServiceLabels{
			"infra/metrics": {
				Service: map[string]string{
					labelServiceType: "monitoring",
				},
			},
		},
		errByService: map[string]error{
			"infra/metrics": errors.New("inspect image labels timeout"),
		},
	}
	subscriber := NewSubscriber(store, inspector, NewResolver(nil))

	err = subscriber.Handle(context.Background(), &events.DeploySuccess{
		StackName: "infra",
		Services: []compose.Service{
			{
				Name:  "metrics",
				Image: "ghcr.io/acme/metrics:0.3",
			},
		},
	})
	require.NoError(t, err, "handle deploy success despite inspect error")

	items := store.List()
	require.Len(t, items, 1, "expected one saved service")
	assert.Equal(t, TypeMonitoring, items[0].Type, "expected type from service labels")
}

func TestSubscriberHandleIgnoresNonDeployEvents(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "services.json"))
	require.NoError(t, err, "new store")

	subscriber := NewSubscriber(store, nil, NewResolver(nil))
	err = subscriber.Handle(context.Background(), &events.SyncManualStarted{})
	require.NoError(t, err, "ignore non-deploy events")

	assert.Empty(t, store.List(), "expected no stored services")
}
