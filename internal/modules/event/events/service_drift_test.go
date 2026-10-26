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
		{name: "restored", event: &ServiceRestored{StackName: "app", ServiceName: "api", Commit: "abc"}, type_: TypeServiceRestored},
		{name: "restore failed", event: &ServiceRestoreFailed{StackName: "app", ServiceName: "api", Commit: "abc"}, type_: TypeServiceRestoreFailed},
		{name: "replicas diverged", event: &ServiceReplicasDiverged{StackName: "app", ServiceName: "api", Commit: "abc"}, type_: TypeServiceReplicasDiverged},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.type_, tt.event.Type())
			assert.Equal(t, map[string]string{
				AttributeStackName: "app", AttributeServiceName: "api", AttributeCommit: "abc",
			}, tt.event.Details())
			assert.NotEmpty(t, tt.event.Message())
		})
	}
}
