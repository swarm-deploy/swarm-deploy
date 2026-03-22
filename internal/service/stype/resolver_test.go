package stype

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolverResolvePriority(t *testing.T) {
	resolver := NewResolver(map[string]string{
		"postgres": Database,
		"traefik":  ReverseProxy,
	})

	cases := []struct {
		name     string
		image    string
		labels   Labels
		expected string
	}{
		{
			name:  "service label has top priority",
			image: "docker.io/library/postgres:16",
			labels: Labels{
				Service: map[string]string{
					LabelService: Monitoring,
				},
				Container: map[string]string{
					LabelService: Database,
				},
			},
			expected: Monitoring,
		},
		{
			name:  "container label used when service label is invalid",
			image: "docker.io/library/postgres:16",
			labels: Labels{
				Service: map[string]string{
					LabelService: "invalid",
				},
				Container: map[string]string{
					LabelService: Delivery,
				},
			},
			expected: Delivery,
		},
		{
			name:     "dictionary value used from image name",
			image:    "registry.example.com/team/postgres:16",
			labels:   Labels{},
			expected: Database,
		},
		{
			name:     "reverse proxy dictionary value used from image name",
			image:    "docker.io/library/traefik:3.0",
			labels:   Labels{},
			expected: ReverseProxy,
		},
		{
			name:     "defaults to application when no strategy matches",
			image:    "registry.example.com/team/custom-worker:1",
			labels:   Labels{},
			expected: Application,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, resolver.Resolve(tc.image, tc.labels), "unexpected type")
		})
	}
}

func TestParse(t *testing.T) {
	typeValue, ok := Parse(" MonItoring ")
	assert.True(t, ok, "expected valid type")
	assert.Equal(t, Monitoring, typeValue, "expected normalized type")

	typeValue, ok = Parse("custom")
	assert.False(t, ok, "expected invalid type")
	assert.Empty(t, typeValue, "expected empty type for invalid value")

	typeValue, ok = Parse("reverse_proxy")
	assert.True(t, ok, "expected reverse proxy alias type")
	assert.Equal(t, ReverseProxy, typeValue, "expected reverse proxy normalized type")
}
