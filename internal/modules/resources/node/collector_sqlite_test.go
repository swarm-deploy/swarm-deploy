package node

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestSnapshotRollsBackWhenPublicationFails(t *testing.T) {
	ctx := context.Background()
	previous := []swarm.Node{{ID: "known", Status: "ready"}}
	c, inspector, publisher, store := newCollectorForTest(t, previous)
	inspector.EXPECT().List(gomock.Any()).Return([]swarm.Node{{ID: "known", Status: "down"}}, nil)
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(errors.New("SQL failed"))
	require.Error(t, c.handleEvent(ctx, swarm.NodeEvent{Action: swarm.EventActionUpdate, NodeID: "known"}))
	actual, err := store.ReadAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, previous, actual)
}

func TestCreateForKnownNodeDoesNotRepublishJoined(t *testing.T) {
	ctx := context.Background()
	previous := []swarm.Node{{ID: "known", Status: "ready"}}
	c, inspector, publisher, store := newCollectorForTest(t, previous)
	require.NoError(t, store.ReplaceSnapshot(ctx, nil)) // disappear without forgetting membership
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.NodeConnected{})).Return(nil)
	inspector.EXPECT().List(gomock.Any()).Return(previous, nil)
	require.NoError(t, c.handleEvent(ctx, swarm.NodeEvent{Action: swarm.EventActionCreate, NodeID: "known"}))
	actual, err := store.ReadAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, previous, actual)
}
