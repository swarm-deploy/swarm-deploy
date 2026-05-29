package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/service/stype"
)

func TestMetadataExtractorResolveTypePriority(t *testing.T) {
	resolver := NewMetadataExtractor()

	cases := []struct {
		name     string
		image    string
		labels   Labels
		expected serviceType.Type
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
			expected: serviceType.Monitoring,
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
			expected: serviceType.Delivery,
		},
		{
			name:     "dictionary value used from image name",
			image:    "registry.example.com/team/postgres:16",
			labels:   Labels{},
			expected: serviceType.Database,
		},
		{
			name:     "reverse proxy dictionary value used from image name",
			image:    "docker.io/library/traefik:3.0",
			labels:   Labels{},
			expected: serviceType.ReverseProxy,
		},
		{
			name:     "defaults to application when no strategy matches",
			image:    "registry.example.com/team/custom-worker:1",
			labels:   Labels{},
			expected: serviceType.Application,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := resolver.Resolve(tc.image, tc.labels)
			assert.Equal(t, tc.expected, metadata.Type, "unexpected type")
		})
	}
}
