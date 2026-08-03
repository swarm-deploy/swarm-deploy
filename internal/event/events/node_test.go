package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNodeEventsDetails(t *testing.T) {
	tests := []struct {
		name            string
		event           Event
		expectedType    Type
		expectedMessage string
		expectedDetails map[string]string
	}{
		{
			name: "connected",
			event: &NodeConnected{
				NodeID:   "node-1",
				NodeName: "worker-1",
				Status:   "ready",
			},
			expectedType:    TypeNodeConnected,
			expectedMessage: "Node worker-1 connected",
			expectedDetails: map[string]string{
				"node_id":   "node-1",
				"node_name": "worker-1",
				"status":    "ready",
			},
		},
		{
			name: "disconnected",
			event: &NodeDisconnected{
				NodeID:   "node-2",
				NodeName: "worker-2",
				Status:   "disconnected",
			},
			expectedType:    TypeNodeDisconnected,
			expectedMessage: "Node worker-2 disconnected",
			expectedDetails: map[string]string{
				"node_id":   "node-2",
				"node_name": "worker-2",
				"status":    "disconnected",
			},
		},
		{
			name: "falls back to node id in message",
			event: &NodeConnected{
				NodeID: "node-3",
				Status: "ready",
			},
			expectedType:    TypeNodeConnected,
			expectedMessage: "Node node-3 connected",
			expectedDetails: map[string]string{
				"node_id": "node-3",
				"status":  "ready",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedType, tt.event.Type(), "unexpected event type")
			assert.Equal(t, tt.expectedMessage, tt.event.Message(), "unexpected event message")
			assert.Equal(t, tt.expectedDetails, tt.event.Details(), "unexpected event details")
		})
	}
}

func TestNodeEventTypesAreRegistered(t *testing.T) {
	tests := []struct {
		name         string
		typeName     TypeName
		expectedType Type
	}{
		{
			name:         "node connected",
			typeName:     TypeNameNodeConnected,
			expectedType: TypeNodeConnected,
		},
		{
			name:         "node disconnected",
			typeName:     TypeNameNodeDisconnected,
			expectedType: TypeNodeDisconnected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, ok := ParseType(tt.expectedType.String())

			assert.True(t, tt.typeName.Valid(), "expected event type name to be valid")
			assert.True(t, ok, "expected event type to be parsed")
			assert.Equal(t, tt.expectedType, parsed, "unexpected parsed type")
			assert.Contains(t, Types, tt.expectedType, "expected event type to be registered")
			assert.Equal(t, CategorySwarm, tt.expectedType.Category(), "unexpected event category")
		})
	}

	parsedCategory, ok := ParseCategory("swarm")
	assert.True(t, ok, "expected swarm category to be parsed")
	assert.Equal(t, CategorySwarm, parsedCategory, "unexpected parsed category")
}
