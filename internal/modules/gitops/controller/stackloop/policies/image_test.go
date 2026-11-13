package policies

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestImagePolicyViolation(t *testing.T) {
	digest := "nginx@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	latestDigest := "nginx:latest@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		name    string
		policy  config.ImagePolicySpec
		image   string
		code    string
		message string
	}{
		{name: "disabled allows malformed", image: "%%%"},
		{name: "required allows tag", policy: imagePolicy(true, false, false), image: "nginx:1.27"},
		{name: "required allows digest", policy: imagePolicy(true, false, false), image: digest},
		{name: "required rejects untagged", policy: imagePolicy(true, false, false), image: "nginx",
			code: ImageViolationRequired, message: "image must have an explicit tag or digest"},
		{name: "no latest allows explicit version", policy: imagePolicy(false, true, false), image: "nginx:1.27"},
		{name: "no latest allows digest", policy: imagePolicy(false, true, false), image: digest},
		{name: "no latest rejects explicit latest", policy: imagePolicy(false, true, false), image: "nginx:latest",
			code: ImageViolationLatest, message: "image must not use the latest tag"},
		{name: "no latest rejects explicit latest with digest", policy: imagePolicy(false, true, false),
			image: latestDigest, code: ImageViolationLatest, message: "image must not use the latest tag"},
		{name: "no latest rejects implicit latest", policy: imagePolicy(false, true, false), image: "nginx",
			code: ImageViolationLatest, message: "image must not use the latest tag"},
		{name: "only sha rejects tag", policy: imagePolicy(false, false, true), image: "nginx:1.27",
			code: ImageViolationOnlySHA, message: "image must use a digest"},
		{name: "only sha allows digest", policy: imagePolicy(false, false, true), image: digest},
		{name: "enabled policy rejects malformed", policy: imagePolicy(true, false, false), image: "%%%",
			code: ImageViolationInvalid, message: "image reference is invalid"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			issues := NewImageChecker(testCase.policy).Check(context.Background(), compose.Service{
				Name: "api", Image: testCase.image,
			})
			if testCase.code == "" {
				assert.Empty(t, issues)
				return
			}
			assert.Equal(t, []compose.ValidationIssue{{
				ResourceType: "service",
				ResourceName: "api",
				Field:        "image",
				Code:         testCase.code,
				Message:      testCase.message,
			}}, issues)
		})
	}
}

func TestCheckImagePolicyValidatesInitJobs(t *testing.T) {
	digest := "example/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	jobDigest := "example/migrate@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name    string
		policy  config.ImagePolicySpec
		image   string
		code    string
		message string
	}{
		{name: "required", policy: imagePolicy(true, false, false), image: "example/migrate",
			code: ImageViolationRequired, message: "image must have an explicit tag or digest"},
		{name: "no latest", policy: imagePolicy(false, true, false), image: "example/migrate:latest",
			code: ImageViolationLatest, message: "image must not use the latest tag"},
		{name: "only sha", policy: imagePolicy(false, false, true), image: "example/migrate:1.2.3",
			code: ImageViolationOnlySHA, message: "image must use a digest"},
		{name: "no latest allows digest only", policy: imagePolicy(false, true, false), image: jobDigest},
		{name: "disabled", image: "%%%"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			issues := NewImageChecker(testCase.policy).Check(context.Background(), compose.Service{
				Name: "api", Image: digest, InitJobs: []compose.InitJob{{Name: "migrate", Image: testCase.image}},
			})
			if testCase.code == "" {
				assert.Empty(t, issues)
				return
			}
			assert.Equal(t, []compose.ValidationIssue{{
				ResourceType: "service",
				ResourceName: "api",
				Field:        `x-init-deploy-jobs["migrate"].image`,
				Code:         testCase.code,
				Message:      `init job "migrate": ` + testCase.message,
			}}, issues)
		})
	}
}

func imagePolicy(required, noLatest, onlySHA bool) config.ImagePolicySpec {
	return config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{
		Required: required, NoLatest: noLatest, OnlySHA: onlySHA,
	}}
}
