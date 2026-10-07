package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeployDeniedDetails(t *testing.T) {
	event := &DeployDenied{
		StackName:   "payments",
		ServiceName: "api",
		Image:       "nginx:latest",
		Policy:      "image.tag.no_latest",
	}

	assert.Equal(t, TypeDeployDenied, event.Type())
	assert.Equal(t, "Deploy denied for service payments/api", event.Message())
	assert.Equal(t, map[string]string{
		"stack_name":   "payments",
		"service_name": "api",
		"image":        "nginx:latest",
		"policy":       "image.tag.no_latest",
	}, event.Details())
}
