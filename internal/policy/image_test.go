package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImagePolicyCheckImage(t *testing.T) {
	tests := []struct {
		name   string
		policy ImagePolicySpec
		image  string
		want   string
	}{
		{
			name:   "allows tagged image",
			policy: ImagePolicySpec{Tag: ImageTagPolicySpec{Required: true}},
			image:  "nginx:1.27",
		},
		{
			name:   "requires explicit tag or digest",
			policy: ImagePolicySpec{Tag: ImageTagPolicySpec{Required: true}},
			image:  "nginx",
			want:   "image.tag.required",
		},
		{
			name:   "rejects latest",
			policy: ImagePolicySpec{Tag: ImageTagPolicySpec{NoLatest: true}},
			image:  "nginx:latest",
			want:   "image.tag.no_latest",
		},
		{
			name:   "rejects implicit latest",
			policy: ImagePolicySpec{Tag: ImageTagPolicySpec{NoLatest: true}},
			image:  "nginx",
			want:   "image.tag.no_latest",
		},
		{
			name:   "requires digest",
			policy: ImagePolicySpec{Tag: ImageTagPolicySpec{OnlySHA: true}},
			image:  "nginx:1.27",
			want:   "image.tag.only_sha",
		},
		{
			name:   "allows digest",
			policy: ImagePolicySpec{Tag: ImageTagPolicySpec{OnlySHA: true}},
			image:  "nginx@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.policy.CheckImage(tt.image))
		})
	}
}
