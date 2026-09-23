package srvcomparator

import (
	"reflect"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

type VolumeComparator struct{}

func (c *VolumeComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.Volumes = c.CompareVolumes(left.Volumes, right.Volumes)
}

func (c *VolumeComparator) CompareVolumes(leftVolumes, rightVolumes compose.ServiceVolumes) []diff.VolumeDiff {
	matchedRight := make([]bool, len(rightVolumes.Volumes))
	diffs := make([]diff.VolumeDiff, 0, len(leftVolumes.Volumes)+len(rightVolumes.Volumes))
	for _, leftVolume := range leftVolumes.Volumes {
		if leftVolume == nil {
			continue
		}
		match := findVolume(leftVolume, rightVolumes.Volumes, matchedRight)
		if match >= 0 {
			matchedRight[match] = true
			continue
		}
		diffs = append(diffs, volumeDiff(leftVolume, false))
	}
	for i, rightVolume := range rightVolumes.Volumes {
		if rightVolume != nil && !matchedRight[i] {
			diffs = append(diffs, volumeDiff(rightVolume, true))
		}
	}

	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Target == diffs[j].Target {
			if diffs[i].Source == diffs[j].Source {
				if diffs[i].Added != diffs[j].Added {
					return boolScore(diffs[i].Added) > boolScore(diffs[j].Added)
				}
				return stableJSON(diffs[i]) < stableJSON(diffs[j])
			}
			return diffs[i].Source < diffs[j].Source
		}
		return diffs[i].Target < diffs[j].Target
	})

	return diffs
}

func findVolume(volume *compose.ServiceVolume, candidates []*compose.ServiceVolume, matched []bool) int {
	for i, candidate := range candidates {
		if !matched[i] && candidate != nil && volumesEqual(volume, candidate) {
			return i
		}
	}
	return -1
}

func volumesEqual(left, right *compose.ServiceVolume) bool {
	return left.Type == right.Type &&
		left.Source == right.Source &&
		left.Target == right.Target &&
		left.Consistency == right.Consistency &&
		left.ReadOnly == right.ReadOnly &&
		reflect.DeepEqual(left.Bind, right.Bind) &&
		reflect.DeepEqual(left.Volume, right.Volume) &&
		reflect.DeepEqual(left.Tmpfs, right.Tmpfs) &&
		reflect.DeepEqual(left.Extra, right.Extra)
}

func volumeDiff(volume *compose.ServiceVolume, added bool) diff.VolumeDiff {
	return diff.VolumeDiff{
		Type:        string(volume.Type),
		Source:      volume.Source,
		Target:      volume.Target,
		ReadOnly:    volume.ReadOnly,
		Consistency: string(volume.Consistency),
		Added:       added,
		Removed:     !added,
	}
}
