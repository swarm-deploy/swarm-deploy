package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceMissedDetails(t *testing.T) {
	tests := []struct {
		name            string
		event           *ServiceMissed
		expectedMessage string
		expectedDetails map[string]string
	}{
		{
			name: "builds message and details",
			event: &ServiceMissed{
				StackName:   "payments",
				ServiceName: "worker",
				Commit:      "abc123",
			},
			expectedMessage: "Service payments/worker missed",
			expectedDetails: map[string]string{
				"stack_name":   "payments",
				"service_name": "worker",
				"commit":       "abc123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, TypeServiceMissed, tt.event.Type(), "unexpected event type")
			assert.Equal(t, tt.expectedMessage, tt.event.Message(), "unexpected event message")
			assert.Equal(t, tt.expectedDetails, tt.event.Details(), "unexpected event details")
		})
	}
}
