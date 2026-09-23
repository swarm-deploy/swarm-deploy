package srvcomparator

import (
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

type SecretComparator struct{}

func (c *SecretComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.Secrets = c.CompareSecrets(left.Secrets, right.Secrets)
}

func (c *SecretComparator) CompareSecrets(
	leftSecrets []compose.ObjectRef,
	rightSecrets []compose.ObjectRef,
) []diff.SecretDiff {
	matchedRight := make([]bool, len(rightSecrets))
	diffs := make([]diff.SecretDiff, 0, len(leftSecrets)+len(rightSecrets))
	for _, leftRef := range leftSecrets {
		match := findConfig(leftRef, rightSecrets, matchedRight)
		if match >= 0 {
			matchedRight[match] = true
			continue
		}
		diffs = append(diffs, secretDiff(leftRef, false))
	}
	for i, rightRef := range rightSecrets {
		if !matchedRight[i] {
			diffs = append(diffs, secretDiff(rightRef, true))
		}
	}

	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Name == diffs[j].Name {
			if diffs[i].MountFile == diffs[j].MountFile {
				if diffs[i].Added != diffs[j].Added {
					return boolScore(diffs[i].Added) > boolScore(diffs[j].Added)
				}
				return stableJSON(diffs[i]) < stableJSON(diffs[j])
			}
			return diffs[i].MountFile < diffs[j].MountFile
		}
		return diffs[i].Name < diffs[j].Name
	})

	return diffs
}

func secretDiff(ref compose.ObjectRef, added bool) diff.SecretDiff {
	return diff.SecretDiff{
		Name:      ref.Source,
		MountFile: ref.Target,
		UID:       ref.Uid,
		GID:       ref.Gid,
		Mode:      fileMode(ref),
		Added:     added,
		Removed:   !added,
	}
}
