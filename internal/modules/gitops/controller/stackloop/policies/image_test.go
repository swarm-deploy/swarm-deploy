package policies

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"testing"
)

func TestImagePolicyViolation(t *testing.T) {
	digest := "nginx@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	latestDigest := "nginx:latest@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		name   string
		policy config.ImagePolicySpec
		image  string
		want   string
	}{
		{name: "disabled allows malformed", image: "%%%"},
		{name: "required allows tag", policy: imagePolicy(true, false, false), image: "nginx:1.27"},
		{name: "required allows digest", policy: imagePolicy(true, false, false), image: digest},
		{name: "required rejects untagged", policy: imagePolicy(true, false, false), image: "nginx",
			want: ImageViolationRequired},
		{name: "no latest allows explicit version", policy: imagePolicy(false, true, false), image: "nginx:1.27"},
		{name: "no latest allows digest", policy: imagePolicy(false, true, false), image: digest},
		{name: "no latest rejects explicit latest", policy: imagePolicy(false, true, false), image: "nginx:latest",
			want: ImageViolationLatest},
		{name: "no latest rejects explicit latest with digest", policy: imagePolicy(false, true, false),
			image: latestDigest, want: ImageViolationLatest},
		{name: "no latest rejects implicit latest", policy: imagePolicy(false, true, false), image: "nginx",
			want: ImageViolationLatest},
		{name: "only sha rejects tag", policy: imagePolicy(false, false, true), image: "nginx:1.27",
			want: ImageViolationOnlySHA},
		{name: "only sha allows digest", policy: imagePolicy(false, false, true), image: digest},
		{name: "enabled policy rejects malformed", policy: imagePolicy(true, false, false), image: "%%%",
			want: ImageViolationInvalid},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			issues := NewImageChecker(testCase.policy).Check(context.Background(), compose.Service{Image: testCase.image})
			if testCase.want == "" {
				assert.Empty(t, issues)
			} else {
				require.Len(t, issues, 1)
				assert.Equal(t, testCase.want, issues[0].Code)
			}
		})
	}
}

func TestCheckImagePolicyValidatesInitJobs(t *testing.T) {
	digest := "example/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	jobDigest := "example/migrate@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name   string
		policy config.ImagePolicySpec
		image  string
		code   string
	}{
		{name: "required", policy: imagePolicy(true, false, false), image: "example/migrate",
			code: ImageViolationRequired},
		{name: "no latest", policy: imagePolicy(false, true, false), image: "example/migrate:latest",
			code: ImageViolationLatest},
		{name: "only sha", policy: imagePolicy(false, false, true), image: "example/migrate:1.2.3",
			code: ImageViolationOnlySHA},
		{name: "no latest allows digest only", policy: imagePolicy(false, true, false), image: jobDigest},
		{name: "disabled", image: "%%%"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			issues := NewImageChecker(testCase.policy).Check(context.Background(), compose.Service{
				Name: "api", Image: digest, InitJobs: []compose.InitJob{{Name: "migrate", Image: testCase.image}},
			})
			if testCase.code == "" {
				require.Empty(t, issues)
				return
			}
			require.Len(t, issues, 1)
			assert.Equal(t, "service", issues[0].ResourceType)
			assert.Equal(t, "api", issues[0].ResourceName)
			assert.Equal(t, `x-init-deploy-jobs["migrate"].image`, issues[0].Field)
			assert.Equal(t, testCase.code, issues[0].Code)
			assert.Contains(t, issues[0].Message, `init job "migrate"`)
			assert.NotContains(t, issues[0].Message, testCase.image)
		})
	}
}

func imagePolicy(required, noLatest, onlySHA bool) config.ImagePolicySpec {
	return config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{
		Required: required, NoLatest: noLatest, OnlySHA: onlySHA,
	}}
}
