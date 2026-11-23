package utils

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
			assert.Equal(t, tc.want, ImageVersion(tc.image))
		})
	}
}

func TestImageName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		image string
		want  string
	}{
		{
			name:  "empty image",
			image: "",
			want:  "",
		},
		{
			name:  "simple tagged image",
			image: "nginx:1.27",
			want:  "nginx",
		},
		{
			name:  "registry path image",
			image: "registry.example.com/ns/app:v2.0",
			want:  "app",
		},
		{
			name:  "image with digest",
			image: "docker.io/library/nginx@sha256:e4720093adc6159c5edefdd39f35fefb1dc33dc99bc1f110e40d250474f288d0",
			want:  "nginx",
		},
		{
			name:  "image with spaces",
			image: "  ghcr.io/Example/Worker:1.0  ",
			want:  "worker",
		},
		{
			name:  "invalid reference fallback",
			image: "LOCALHOST/My-App:dev",
			want:  "my-app",
		},
		{
			name:  "image without tag",
			image: "docker.io/library/alpine",
			want:  "alpine",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ImageName(tc.image))
		})
	}
}
