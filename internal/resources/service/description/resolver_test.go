package description

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolverResolvePriority(t *testing.T) {
	resolver := NewResolver()

	cases := []struct {
		name        string
		labels      Labels
		description string
	}{
		{
			name: "service label has top priority",
			labels: Labels{
				Service: map[string]string{
					LabelService: "Service description",
				},
				Container: map[string]string{
					LabelService: "Container description",
				},
				Image: map[string]string{
					LabelImageTitle:       "Image title",
					LabelImageDescription: "Image description",
				},
			},
			description: "Service description",
		},
		{
			name: "container label is used when service label is absent",
			labels: Labels{
				Container: map[string]string{
					LabelService: "Container description",
				},
				Image: map[string]string{
					LabelImageTitle:       "Image title",
					LabelImageDescription: "Image description",
				},
			},
			description: "Container description",
		},
		{
			name: "service image title is used before container and image labels",
			labels: Labels{
				Service: map[string]string{
					LabelImageTitle: "Service image title",
				},
				Container: map[string]string{
					LabelImageTitle: "Container image title",
				},
				Image: map[string]string{
					LabelImageTitle: "Image title",
				},
			},
			description: "Service image title",
		},
		{
			name: "container image description is used when title is absent",
			labels: Labels{
				Container: map[string]string{
					LabelImageDescription: "Container image description",
				},
				Image: map[string]string{
					LabelImageDescription: "Image description",
				},
			},
			description: "Container image description",
		},
		{
			name: "image description is used before image title",
			labels: Labels{
				Image: map[string]string{
					LabelImageTitle:       "Image title",
					LabelImageDescription: "Image description",
				},
			},
			description: "Image description",
		},
		{
			name: "image description is used when title is absent",
			labels: Labels{
				Image: map[string]string{
					LabelImageDescription: "Image description",
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
			assert.Equal(t, tc.description, resolver.Resolve(tc.labels), "unexpected description")
		})
	}
}
