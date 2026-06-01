package srvcomparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/differ/diff"
)

func TestServiceSecretComparatorCompareSecrets(t *testing.T) {
	comparator := &SecretComparator{}

	testCases := []struct {
		name      string
		left      []compose.ObjectRef
		right     []compose.ObjectRef
		expecteds []diff.SecretDiff
	}{
		{
			name: "detects added and removed secrets",
			left: []compose.ObjectRef{
				{Source: "app-secret", Target: "/run/secrets/app-secret"},
				{Source: "legacy-secret", Target: "/run/secrets/legacy-secret"},
			},
			right: []compose.ObjectRef{
				{Source: "app-secret", Target: "/run/secrets/app-secret-v2"},
				{Source: "current-secret", Target: "/run/secrets/current-secret"},
			},
			expecteds: []diff.SecretDiff{
				{Name: "app-secret", MountFile: "/run/secrets/app-secret", Removed: true},
				{Name: "app-secret", MountFile: "/run/secrets/app-secret-v2", Added: true},
				{Name: "current-secret", MountFile: "/run/secrets/current-secret", Added: true},
				{Name: "legacy-secret", MountFile: "/run/secrets/legacy-secret", Removed: true},
			},
		},
		{
			name: "returns empty for equal secrets",
			left: []compose.ObjectRef{
				{Source: "db-pass", Target: "/run/secrets/db-pass"},
			},
			right: []compose.ObjectRef{
				{Source: "db-pass", Target: "/run/secrets/db-pass"},
			},
			expecteds: []diff.SecretDiff{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			diffs := comparator.CompareSecrets(testCase.left, testCase.right)

			assert.Equal(t, testCase.expecteds, diffs, "unexpected secret changes")
		})
	}
}

func TestServiceSecretComparatorCompareSetsSecretDiff(t *testing.T) {
	comparator := &SecretComparator{}

	leftService := compose.Service{
		Secrets: []compose.ObjectRef{
			{Source: "app-secret", Target: "/run/secrets/app-secret"},
		},
	}
	rightService := compose.Service{
		Secrets: []compose.ObjectRef{
			{Source: "app-secret", Target: "/run/secrets/app-secret-v2"},
		},
	}

	serviceDiff := &diff.ServiceDiff{
		Secrets: []diff.SecretDiff{
			{Name: "old-secret", Added: true},
		},
	}

	comparator.Compare(leftService, rightService, serviceDiff)

	assert.Equal(
		t,
		[]diff.SecretDiff{
			{Name: "app-secret", MountFile: "/run/secrets/app-secret", Removed: true},
			{Name: "app-secret", MountFile: "/run/secrets/app-secret-v2", Added: true},
		},
		serviceDiff.Secrets,
		"compare must write secret diff to service diff",
	)
}
