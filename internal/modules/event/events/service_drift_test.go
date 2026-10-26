package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceDriftEventDetails(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		type_ Type
	}{
		{name: "drifted", event: &ServiceDrifted{
			StackName: "app", ServiceName: "api", Commit: "abc", Reason: "replicas differ",
		}, type_: TypeServiceDrifted},
		{name: "fixed", event: &ServiceDriftFixed{
			StackName: "app", ServiceName: "api", Commit: "abc",
		}, type_: TypeServiceDriftFixed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.type_, tt.event.Type())
			assert.Equal(t, "app", tt.event.Details()[AttributeStackName])
			assert.Equal(t, "api", tt.event.Details()[AttributeServiceName])
			assert.Equal(t, "abc", tt.event.Details()[AttributeCommit])
			assert.NotEmpty(t, tt.event.Message())
		})
	}
}
