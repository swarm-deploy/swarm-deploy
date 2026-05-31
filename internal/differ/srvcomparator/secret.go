package srvcomparator

import (
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/differ/diff"
)

type SecretComparator struct{}

func (c *SecretComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.Secrets = c.CompareSecrets(left.Secrets, right.Secrets)
}

func (c *SecretComparator) CompareSecrets(
	leftSecrets []compose.ObjectRef,
	rightSecrets []compose.ObjectRef,
) []diff.SecretDiff {
	leftSet := mapSecretRefs(leftSecrets)
	rightSet := mapSecretRefs(rightSecrets)

	keys := map[string]struct{}{}
	for key := range leftSet {
		keys[key] = struct{}{}
	}
	for key := range rightSet {
		keys[key] = struct{}{}
	}

	sortedKeys := mapKeys(keys)
	sort.Strings(sortedKeys)

	diffs := make([]diff.SecretDiff, 0, len(sortedKeys))
	for _, key := range sortedKeys {
		leftRef, leftExists := leftSet[key]
		rightRef, rightExists := rightSet[key]

		switch {
		case !leftExists && rightExists:
			diffs = append(diffs, diff.SecretDiff{
				Name:      rightRef.Source,
				MountFile: rightRef.Target,
				Added:     true,
			})
		case leftExists && !rightExists:
			diffs = append(diffs, diff.SecretDiff{
				Name:      leftRef.Source,
				MountFile: leftRef.Target,
				Removed:   true,
			})
		}
	}

	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Name == diffs[j].Name {
			if diffs[i].MountFile == diffs[j].MountFile {
				return boolScore(diffs[i].Added) > boolScore(diffs[j].Added)
			}
			return diffs[i].MountFile < diffs[j].MountFile
		}
		return diffs[i].Name < diffs[j].Name
	})

	return diffs
}

func mapSecretRefs(secrets []compose.ObjectRef) map[string]compose.ObjectRef {
	set := map[string]compose.ObjectRef{}
	for _, secret := range secrets {
		key := secret.Source + ":" + secret.Target
		set[key] = secret
	}

	return set
}
