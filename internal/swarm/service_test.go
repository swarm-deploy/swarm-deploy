package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceReferenceName(t *testing.T) {
	ref := NewServiceReference("core", "api")

	assert.Equal(t, "core_api", ref.Name(), "unexpected full service name")
	assert.Equal(t, "core", ref.StackName(), "unexpected stack name")
	assert.Equal(t, "api", ref.ServiceName(), "unexpected service name")
}
