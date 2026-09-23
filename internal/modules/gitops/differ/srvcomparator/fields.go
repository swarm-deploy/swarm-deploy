package srvcomparator

import (
	"maps"
	"reflect"
	"slices"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

// CommandComparator compares service commands.
type CommandComparator struct{}

// Compare writes a command difference to srvDiff.
func (*CommandComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	if slices.Equal(left.Command.Args, right.Command.Args) {
		return
	}

	srvDiff.Command = &diff.CommandDiff{
		Old: slices.Clone(left.Command.Args),
		New: slices.Clone(right.Command.Args),
	}
}

// HealthcheckComparator compares service healthchecks.
type HealthcheckComparator struct{}

// Compare writes a healthcheck difference to srvDiff.
func (*HealthcheckComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	if reflect.DeepEqual(left.Healthcheck, right.Healthcheck) {
		return
	}

	srvDiff.Healthcheck = &diff.ValueDiff[*compose.ServiceHealth]{Old: left.Healthcheck, New: right.Healthcheck}
}

// DeployComparator compares service deploy settings.
type DeployComparator struct{}

// Compare writes a deploy difference to srvDiff.
func (*DeployComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	if reflect.DeepEqual(left.Deploy, right.Deploy) {
		return
	}

	srvDiff.Deploy = &diff.ValueDiff[compose.ServiceDeploy]{Old: left.Deploy, New: right.Deploy}
}

// CapabilitiesComparator compares service cap_add and cap_drop sets.
type CapabilitiesComparator struct{}

// Compare writes capability differences to srvDiff.
func (*CapabilitiesComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	leftAdd, rightAdd := sortedStrings(left.CapAdd), sortedStrings(right.CapAdd)
	if !slices.Equal(leftAdd, rightAdd) {
		srvDiff.CapAdd = &diff.ValueDiff[[]string]{Old: leftAdd, New: rightAdd}
	}

	leftDrop, rightDrop := sortedStrings(left.CapDrop), sortedStrings(right.CapDrop)
	if !slices.Equal(leftDrop, rightDrop) {
		srvDiff.CapDrop = &diff.ValueDiff[[]string]{Old: leftDrop, New: rightDrop}
	}
}

// LabelsComparator compares service labels.
type LabelsComparator struct{}

// Compare writes a labels difference to srvDiff.
func (*LabelsComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	if maps.Equal(left.Labels.Map, right.Labels.Map) {
		return
	}

	srvDiff.Labels = &diff.ValueDiff[map[string]string]{
		Old: maps.Clone(left.Labels.Map),
		New: maps.Clone(right.Labels.Map),
	}
}

// LoggingComparator compares service logging settings.
type LoggingComparator struct{}

// Compare writes a logging difference to srvDiff.
func (*LoggingComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	if reflect.DeepEqual(left.Logging, right.Logging) {
		return
	}

	srvDiff.Logging = &diff.ValueDiff[compose.ServiceLogging]{Old: left.Logging, New: right.Logging}
}

// EnvFileComparator compares ordered service env_file lists.
type EnvFileComparator struct{}

// Compare writes an env_file difference to srvDiff.
func (*EnvFileComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	if slices.Equal(left.EnvFiles, right.EnvFiles) {
		return
	}

	srvDiff.EnvFiles = &diff.ValueDiff[[]string]{
		Old: slices.Clone(left.EnvFiles),
		New: slices.Clone(right.EnvFiles),
	}
}

func sortedStrings(values []string) []string {
	result := slices.Clone(values)
	sort.Strings(result)
	return result
}
