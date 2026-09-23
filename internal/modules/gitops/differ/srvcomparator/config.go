package srvcomparator

import (
	"reflect"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

type ConfigComparator struct{}

func (c *ConfigComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.Configs = c.CompareConfigs(left.Configs, right.Configs)
}

func (c *ConfigComparator) CompareConfigs(leftConfigs, rightConfigs []compose.ObjectRef) []diff.ConfigDiff {
	matchedRight := make([]bool, len(rightConfigs))
	diffs := make([]diff.ConfigDiff, 0, len(leftConfigs)+len(rightConfigs))
	for _, leftRef := range leftConfigs {
		match := findConfig(leftRef, rightConfigs, matchedRight)
		if match >= 0 {
			matchedRight[match] = true
			continue
		}
		diffs = append(diffs, configDiff(leftRef, false))
	}
	for i, rightRef := range rightConfigs {
		if !matchedRight[i] {
			diffs = append(diffs, configDiff(rightRef, true))
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

func findConfig(ref compose.ObjectRef, candidates []compose.ObjectRef, matched []bool) int {
	for i, candidate := range candidates {
		if !matched[i] && objectRefsEqual(ref, candidate) {
			return i
		}
	}
	return -1
}

func objectRefsEqual(left, right compose.ObjectRef) bool {
	return left.Source == right.Source &&
		left.Target == right.Target &&
		left.Uid == right.Uid &&
		left.Gid == right.Gid &&
		reflect.DeepEqual(left.Mode, right.Mode) &&
		reflect.DeepEqual(left.Extra, right.Extra)
}

func configDiff(ref compose.ObjectRef, added bool) diff.ConfigDiff {
	return diff.ConfigDiff{
		Name:      ref.Source,
		MountFile: ref.Target,
		UID:       ref.Uid,
		GID:       ref.Gid,
		Mode:      fileMode(ref),
		Added:     added,
		Removed:   !added,
	}
}

func fileMode(ref compose.ObjectRef) *uint32 {
	if ref.Mode == nil {
		return nil
	}

	mode := uint32(*ref.Mode)
	return &mode
}
