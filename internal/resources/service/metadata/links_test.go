package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestLinksResolverResolveOpenAPI(t *testing.T) {
	resolver := NewLinksResolver()

	meta := &Metadata{}
	resolver.Resolve(Labels{
		Service: map[string]string{
			labelsdict.OpenAPIURL: "https://api.example.com/openapi.json",
		},
	}, meta)

	assert.Equal(t, []Link{{
		Type: "OpenAPI",
		URL:  "https://api.example.com/openapi.json",
	}}, meta.Links)
}
