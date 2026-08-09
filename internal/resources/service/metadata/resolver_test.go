package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestResolverResolvePriority(t *testing.T) {
	resolver := NewDescriptionResolver()

	cases := []struct {
		name        string
		labels      Labels
		description string
	}{
		{
			name: "service label has top priority",
			labels: Labels{
				Service: map[string]string{
					labelsdict.ServiceDescription: "Service description",
				},
				Container: map[string]string{
					labelsdict.ServiceDescription: "Container description",
				},
				Image: map[string]string{
					labelsdict.OCIImageTitle:       "Image title",
					labelsdict.OCIImageDescription: "Image description",
				},
			},
			description: "Service description",
		},
		{
			name: "container label is used when service label is absent",
			labels: Labels{
				Container: map[string]string{
					labelsdict.ServiceDescription: "Container description",
				},
				Image: map[string]string{
					labelsdict.OCIImageTitle:       "Image title",
					labelsdict.OCIImageDescription: "Image description",
				},
			},
			description: "Container description",
		},
		{
			name: "service image title is used before container and image labels",
			labels: Labels{
				Service: map[string]string{
					labelsdict.OCIImageTitle: "Service image title",
				},
				Container: map[string]string{
					labelsdict.OCIImageTitle: "Container image title",
				},
				Image: map[string]string{
					labelsdict.OCIImageTitle: "Image title",
				},
			},
			description: "Service image title",
		},
		{
			name: "container image description is used when title is absent",
			labels: Labels{
				Container: map[string]string{
					labelsdict.OCIImageDescription: "Container image description",
				},
				Image: map[string]string{
					labelsdict.OCIImageDescription: "Image description",
				},
			},
			description: "Container image description",
		},
		{
			name: "image description is used before image title",
			labels: Labels{
				Image: map[string]string{
					labelsdict.OCIImageTitle:       "Image title",
					labelsdict.OCIImageDescription: "Image description",
				},
			},
			description: "Image description",
		},
		{
			name: "image description is used when title is absent",
			labels: Labels{
				Image: map[string]string{
					labelsdict.OCIImageDescription: "Image description",
				},
			},
			description: "Image description",
		},
		{
			name:        "empty labels produce empty description",
			labels:      Labels{},
			description: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.description, resolver.resolveDescription(tc.labels), "unexpected description")
		})
	}
}
