package imageref

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		image string
		want  string
	}{
		{"", ""},
		{"nginx:1.27", "1.27"},
		{"docker.io/library/nginx:latest", "latest"},
		{"registry.example.com/ns/app:v2.0", "v2.0"},
		{
			"nginx@sha256:e4720093adc6159c5edefdd39f35fefb1dc33dc99bc1f110e40d250474f288d0",
			"sha256:e4720093adc6",
		},
	}

	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Version(tc.image))
		})
	}
}
