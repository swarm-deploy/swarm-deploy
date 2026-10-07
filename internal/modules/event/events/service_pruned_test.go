package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServicePrunedDetails(t *testing.T) {
	event := &ServicePruned{
		StackName:   "payments",
		ServiceName: "worker",
		Commit:      "abc123",
	}

	assert.Equal(t, TypeServicePruned, event.Type(), "unexpected event type")
	assert.Equal(t, "Service payments/worker pruned", event.Message(), "unexpected event message")
	assert.Equal(
		t,
		map[string]string{
			"stack_name":   "payments",
			"service_name": "worker",
			"commit":       "abc123",
		},
		event.Details(),
		"unexpected event details",
	)
}
