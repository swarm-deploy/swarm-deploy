package syncpolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestCheckImage(t *testing.T) {
	tests := []struct {
		name   string
		policy config.ImagePolicySpec
		image  string
		want   string
	}{
		{
			name:   "allows tagged image",
			policy: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{Required: true}},
			image:  "nginx:1.27",
		},
		{
			name:   "requires explicit tag or digest",
			policy: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{Required: true}},
			image:  "nginx",
			want:   ViolationImageTagRequired,
		},
		{
			name:   "rejects latest",
			policy: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}},
			image:  "nginx:latest",
			want:   ViolationImageTagNoLatest,
		},
		{
			name:   "rejects implicit latest",
			policy: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}},
			image:  "nginx",
			want:   ViolationImageTagNoLatest,
		},
		{
			name:   "requires digest",
			policy: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{OnlySHA: true}},
			image:  "nginx:1.27",
			want:   ViolationImageTagOnlySHA,
		},
		{
			name:   "allows digest",
			policy: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{OnlySHA: true}},
			image:  "nginx@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		},
		{
			name:   "reports malformed image independently of required rule",
			policy: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}},
			image:  "%%%",
			want:   ViolationImageInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CheckImage(tt.policy, tt.image))
		})
	}
}
