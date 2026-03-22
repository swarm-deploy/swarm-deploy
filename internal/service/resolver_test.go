package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolverResolveTypePriority(t *testing.T) {
	resolver := NewResolver(map[string]Type{
		"postgres": TypeDatabase,
		"traefik":  TypeReverseProxy,
	})

	cases := []struct {
		name     string
		image    string
		labels   Labels
		expected Type
	}{
		{
			name:  "service label has top priority",
			image: "docker.io/library/postgres:16",
			labels: Labels{
				Service: map[string]string{
					labelServiceType: "monitoring",
				},
				Container: map[string]string{
					labelServiceType: "database",
				},
			},
			expected: TypeMonitoring,
		},
		{
			name:  "container label used when service label is invalid",
			image: "docker.io/library/postgres:16",
			labels: Labels{
				Service: map[string]string{
					labelServiceType: "invalid",
				},
				Container: map[string]string{
					labelServiceType: "delivery",
				},
			},
			expected: TypeDelivery,
		},
		{
			name:     "dictionary value used from image name",
			image:    "registry.example.com/team/postgres:16",
			labels:   Labels{},
			expected: TypeDatabase,
		},
		{
			name:     "reverse proxy dictionary value used from image name",
			image:    "docker.io/library/traefik:3.0",
			labels:   Labels{},
			expected: TypeReverseProxy,
		},
		{
			name:     "defaults to application when no strategy matches",
			image:    "registry.example.com/team/custom-worker:1",
			labels:   Labels{},
			expected: TypeApplication,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := resolver.Resolve(tc.image, tc.labels)
			assert.Equal(t, tc.expected, metadata.Type, "unexpected type")
		})
	}
}
