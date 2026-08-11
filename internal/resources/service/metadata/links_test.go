package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestLinksResolverResolveOpenAPI(t *testing.T) {
	resolver := NewLinksResolver()

	cases := []struct {
		name     string
		labels   Labels
		expected []Link
	}{
		{
			name: "swarm deploy label",
			labels: Labels{
				Service: map[string]string{
					labelsdict.OpenAPIURL: "https://api.example.com/openapi.json",
				},
			},
			expected: []Link{{Type: "OpenAPI", URL: "https://api.example.com/openapi.json"}},
		},
		{
			name: "traefik api portal label",
			labels: Labels{
				Service: map[string]string{
					"traefik.http.services.myapi-svc.loadbalancer.apiportal.path": "/openapi.json",
				},
			},
			expected: []Link{{Type: "OpenAPI", URL: "/openapi.json"}},
		},
		{
			name: "traefik api portal label from container",
			labels: Labels{
				Container: map[string]string{
					"traefik.http.services.myapi-svc.loadbalancer.apiportal.path": "/swagger.json",
				},
			},
			expected: []Link{{Type: "OpenAPI", URL: "/swagger.json"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := &Metadata{}
			resolver.Resolve(tc.labels, meta)
			assert.ElementsMatch(t, tc.expected, meta.Links)
		})
	}
}
