package srvcomparator

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

func TestServiceSecretComparatorCompareSecrets(t *testing.T) {
	comparator := &SecretComparator{}
	oldMode := os.FileMode(0o440)
	newMode := os.FileMode(0o400)

	testCases := []struct {
		name      string
		left      []compose.ObjectRef
		right     []compose.ObjectRef
		expecteds []diff.SecretDiff
	}{
		{
			name: "detects added removed and fully changed secrets",
			left: []compose.ObjectRef{
				{Source: "app-secret", Target: "/run/secrets/app-secret", Uid: "1000", Gid: "1000", Mode: &oldMode},
				{Source: "extra-secret", Target: "/run/secrets/extra", Extra: map[string]interface{}{"x-driver": "old"}},
				{Source: "legacy-secret", Target: "/run/secrets/legacy-secret"},
			},
			right: []compose.ObjectRef{
				{Source: "app-secret", Target: "/run/secrets/app-secret-v2", Uid: "2000", Gid: "2000", Mode: &newMode},
				{Source: "extra-secret", Target: "/run/secrets/extra", Extra: map[string]interface{}{"x-driver": "new"}},
				{Source: "current-secret", Target: "/run/secrets/current-secret"},
			},
			expecteds: []diff.SecretDiff{
				{Name: "app-secret", MountFile: "/run/secrets/app-secret", UID: "1000", GID: "1000", Mode: uint32Ptr(0o440), Removed: true},
				{Name: "app-secret", MountFile: "/run/secrets/app-secret-v2", UID: "2000", GID: "2000", Mode: uint32Ptr(0o400), Added: true},
				{Name: "current-secret", MountFile: "/run/secrets/current-secret", Added: true},
				{Name: "extra-secret", MountFile: "/run/secrets/extra", Added: true},
				{Name: "extra-secret", MountFile: "/run/secrets/extra", Removed: true},
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
