package srvcomparator

import (
	"slices"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

// CommandComparator compares service commands.
type CommandComparator struct{}

// Compare writes a command difference to srvDiff.
func (*CommandComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.Command = commandDiff(left.Command.Args, right.Command.Args)
}

// HealthcheckComparator compares service healthchecks.
type HealthcheckComparator struct{}

// Compare writes field-level healthcheck differences to srvDiff.
func (*HealthcheckComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	leftHealth := healthcheckValue(left.Healthcheck)
	rightHealth := healthcheckValue(right.Healthcheck)

	result := &diff.HealthcheckDiff{
		Test:          commandDiff(leftHealth.Test.Args, rightHealth.Test.Args),
		Interval:      scalarDiff(leftHealth.Interval, rightHealth.Interval),
		Timeout:       scalarDiff(leftHealth.Timeout, rightHealth.Timeout),
		Retries:       optionalScalarDiff(leftHealth.Retries, rightHealth.Retries),
		StartPeriod:   scalarDiff(leftHealth.StartPeriod, rightHealth.StartPeriod),
		StartInterval: scalarDiff(leftHealth.StartInterval, rightHealth.StartInterval),
		Disable:       scalarDiff(leftHealth.Disable, rightHealth.Disable),
	}
	if result.Test != nil || result.Interval != nil || result.Timeout != nil || result.Retries != nil ||
		result.StartPeriod != nil || result.StartInterval != nil || result.Disable != nil {
		srvDiff.Healthcheck = result
	}
}

// DeployComparator compares service deploy settings.
type DeployComparator struct{}

// Compare writes field-level deploy differences to srvDiff.
func (*DeployComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	result := &diff.DeployDiff{
		EndpointMode:   scalarDiff(left.Deploy.EndpointMode, right.Deploy.EndpointMode),
		Labels:         labelDiffs(left.Deploy.Labels.Map, right.Deploy.Labels.Map),
		Mode:           scalarDiff(left.Deploy.Mode, right.Deploy.Mode),
		Replicas:       optionalScalarDiff(left.Deploy.Replicas, right.Deploy.Replicas),
		Placement:      placementDiff(left.Deploy.Placement, right.Deploy.Placement),
		Resources:      resourcesDiff(left.Deploy.Resources, right.Deploy.Resources),
		RestartPolicy:  restartPolicyDiff(left.Deploy.RestartPolicy, right.Deploy.RestartPolicy),
		RollbackConfig: rollbackConfigDiff(left.Deploy.RollbackConfig, right.Deploy.RollbackConfig),
		UpdateConfig:   updateConfigDiff(left.Deploy.UpdateConfig, right.Deploy.UpdateConfig),
	}
	if result.EndpointMode != nil || len(result.Labels) > 0 || result.Mode != nil || result.Replicas != nil ||
		result.Placement != nil || result.Resources != nil || result.RestartPolicy != nil ||
		result.RollbackConfig != nil || result.UpdateConfig != nil {
		srvDiff.Deploy = result
	}
}

// CapabilitiesComparator compares service cap_add and cap_drop sets.
type CapabilitiesComparator struct{}

// Compare writes capability differences to srvDiff.
func (*CapabilitiesComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.CapAdd = stringSetDiff(left.CapAdd, right.CapAdd)
	srvDiff.CapDrop = stringSetDiff(left.CapDrop, right.CapDrop)
}

// LabelsComparator compares service labels.
type LabelsComparator struct{}

// Compare writes per-key label differences to srvDiff.
func (*LabelsComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	srvDiff.Labels = labelDiffs(left.Labels.Map, right.Labels.Map)
}

// LoggingComparator compares service logging settings.
type LoggingComparator struct{}

// Compare writes field-level logging differences to srvDiff.
func (*LoggingComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	result := &diff.LoggingDiff{
		Driver:  scalarDiff(left.Logging.Driver, right.Logging.Driver),
		Options: loggingOptionDiffs(left.Logging.Options, right.Logging.Options),
	}
	if result.Driver != nil || len(result.Options) > 0 {
		srvDiff.Logging = result
	}
}

// EnvFileComparator compares ordered service env_file lists.
type EnvFileComparator struct{}

// Compare writes an ordered env_file difference to srvDiff.
func (*EnvFileComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	if slices.Equal(left.EnvFiles, right.EnvFiles) {
		return
	}

	srvDiff.EnvFiles = &diff.StringListDiff{Old: slices.Clone(left.EnvFiles), New: slices.Clone(right.EnvFiles)}
}

func commandDiff(left, right []string) *diff.CommandDiff {
	if slices.Equal(left, right) {
		return nil
	}

	return &diff.CommandDiff{Old: slices.Clone(left), New: slices.Clone(right)}
}

func scalarDiff[T comparable](left, right T) *diff.ScalarDiff[T] {
	if left == right {
		return nil
	}

	return &diff.ScalarDiff[T]{Old: left, New: right}
}

func optionalScalarDiff[T comparable](left, right *T) *diff.ScalarDiff[T] {
	if left == nil && right == nil || left != nil && right != nil && *left == *right {
		return nil
	}

	var oldValue, newValue T
	if left != nil {
		oldValue = *left
	}
	if right != nil {
		newValue = *right
	}

	return &diff.ScalarDiff[T]{Old: oldValue, New: newValue}
}

func stringSetDiff(left, right []string) *diff.StringSetDiff {
	leftSet := make(map[string]struct{}, len(left))
	for _, value := range left {
		leftSet[value] = struct{}{}
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}

	result := &diff.StringSetDiff{}
	for value := range rightSet {
		if _, exists := leftSet[value]; !exists {
			result.Added = append(result.Added, value)
		}
	}
	for value := range leftSet {
		if _, exists := rightSet[value]; !exists {
			result.Removed = append(result.Removed, value)
		}
	}
	if len(result.Added) == 0 && len(result.Removed) == 0 {
		return nil
	}

	sort.Strings(result.Added)
	sort.Strings(result.Removed)

	return result
}

func labelDiffs(left, right map[string]string) []diff.LabelDiff {
	keys := changedMapKeys(left, right)
	if len(keys) == 0 {
		return nil
	}
	result := make([]diff.LabelDiff, 0, len(keys))
	for _, key := range keys {
		oldValue, oldExists := left[key]
		newValue, newExists := right[key]
		change := diff.LabelDiff{Key: key, Old: oldValue, New: newValue}
		switch {
		case !oldExists:
			change.Added = true
		case !newExists:
			change.Removed = true
		default:
			change.Changed = true
		}
		result = append(result, change)
	}

	return result
}

func loggingOptionDiffs(left, right map[string]string) []diff.LoggingOptionDiff {
	keys := changedMapKeys(left, right)
	if len(keys) == 0 {
		return nil
	}
	result := make([]diff.LoggingOptionDiff, 0, len(keys))
	for _, key := range keys {
		oldValue, oldExists := left[key]
		newValue, newExists := right[key]
		change := diff.LoggingOptionDiff{Key: key, Old: oldValue, New: newValue}
		switch {
		case !oldExists:
			change.Added = true
		case !newExists:
			change.Removed = true
		default:
			change.Changed = true
		}
		result = append(result, change)
	}

	return result
}

func changedMapKeys(left, right map[string]string) []string {
	keys := make([]string, 0, len(left)+len(right))
	seen := make(map[string]struct{}, len(left)+len(right))
	for key, oldValue := range left {
		seen[key] = struct{}{}
		if newValue, exists := right[key]; !exists || newValue != oldValue {
			keys = append(keys, key)
		}
	}
	for key := range right {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	return keys
}

func healthcheckValue(value *compose.ServiceHealth) compose.ServiceHealth {
	if value == nil {
		return compose.ServiceHealth{}
	}

	return *value
}

func placementDiff(left, right *compose.ServiceDeployPlacement) *diff.PlacementDiff {
	leftValue, rightValue := placementValue(left), placementValue(right)
	result := &diff.PlacementDiff{
		Constraints:        stringSetDiff(leftValue.Constraints, rightValue.Constraints),
		Preferences:        preferenceDiff(leftValue.Preferences, rightValue.Preferences),
		MaxReplicasPerNode: optionalScalarDiff(leftValue.MaxReplicasPerNode, rightValue.MaxReplicasPerNode),
	}
	if result.Constraints == nil && result.Preferences == nil && result.MaxReplicasPerNode == nil {
		return nil
	}

	return result
}

func placementValue(value *compose.ServiceDeployPlacement) compose.ServiceDeployPlacement {
	if value == nil {
		return compose.ServiceDeployPlacement{}
	}

	return *value
}

func preferenceDiff(left, right []compose.ServiceDeployPlacementPreference) *diff.StringListDiff {
	leftValues := make([]string, len(left))
	for index, preference := range left {
		leftValues[index] = preference.Spread
	}
	rightValues := make([]string, len(right))
	for index, preference := range right {
		rightValues[index] = preference.Spread
	}

	if slices.Equal(leftValues, rightValues) {
		return nil
	}

	return &diff.StringListDiff{Old: leftValues, New: rightValues}
}

func resourcesDiff(left, right *compose.ServiceDeployResources) *diff.ResourcesDiff {
	leftValue, rightValue := resourcesValue(left), resourcesValue(right)
	result := &diff.ResourcesDiff{
		Limits:       resourceValuesDiff(leftValue.Limits, rightValue.Limits),
		Reservations: resourceValuesDiff(leftValue.Reservations, rightValue.Reservations),
	}
	if result.Limits == nil && result.Reservations == nil {
		return nil
	}

	return result
}

func resourcesValue(value *compose.ServiceDeployResources) compose.ServiceDeployResources {
	if value == nil {
		return compose.ServiceDeployResources{}
	}

	return *value
}

func resourceValuesDiff(left, right *compose.ServiceDeployResource) *diff.ResourceValuesDiff {
	leftValue, rightValue := resourceValues(left), resourceValues(right)
	result := &diff.ResourceValuesDiff{
		CPUs:   scalarDiff(leftValue.Cpus, rightValue.Cpus),
		Memory: scalarDiff(leftValue.Memory, rightValue.Memory),
		PIDs:   optionalScalarDiff(leftValue.Pids, rightValue.Pids),
	}
	if result.CPUs == nil && result.Memory == nil && result.PIDs == nil {
		return nil
	}

	return result
}

func resourceValues(value *compose.ServiceDeployResource) compose.ServiceDeployResource {
	if value == nil {
		return compose.ServiceDeployResource{}
	}

	return *value
}

func restartPolicyDiff(left, right *compose.ServiceDeployRestartPolicy) *diff.RestartPolicyDiff {
	leftValue, rightValue := restartPolicyValue(left), restartPolicyValue(right)
	result := &diff.RestartPolicyDiff{
		Condition:   scalarDiff(leftValue.Condition, rightValue.Condition),
		Delay:       scalarDiff(leftValue.Delay, rightValue.Delay),
		MaxAttempts: optionalScalarDiff(leftValue.MaxAttempts, rightValue.MaxAttempts),
		Window:      scalarDiff(leftValue.Window, rightValue.Window),
	}
	if result.Condition == nil && result.Delay == nil && result.MaxAttempts == nil && result.Window == nil {
		return nil
	}

	return result
}

func restartPolicyValue(value *compose.ServiceDeployRestartPolicy) compose.ServiceDeployRestartPolicy {
	if value == nil {
		return compose.ServiceDeployRestartPolicy{}
	}

	return *value
}

func updateConfigDiff(left, right *compose.ServiceDeployUpdateConfig) *diff.UpdateConfigDiff {
	leftValue, rightValue := updateConfigValue(left), updateConfigValue(right)
	result := &diff.UpdateConfigDiff{
		Parallelism:     optionalScalarDiff(leftValue.Parallelism, rightValue.Parallelism),
		Delay:           scalarDiff(leftValue.Delay, rightValue.Delay),
		FailureAction:   scalarDiff(leftValue.FailureAction, rightValue.FailureAction),
		Monitor:         scalarDiff(leftValue.Monitor, rightValue.Monitor),
		MaxFailureRatio: optionalScalarDiff(leftValue.MaxFailureRatio, rightValue.MaxFailureRatio),
		Order:           scalarDiff(leftValue.Order, rightValue.Order),
	}
	if result.Parallelism == nil && result.Delay == nil && result.FailureAction == nil &&
		result.Monitor == nil && result.MaxFailureRatio == nil && result.Order == nil {
		return nil
	}

	return result
}

func updateConfigValue(value *compose.ServiceDeployUpdateConfig) compose.ServiceDeployUpdateConfig {
	if value == nil {
		return compose.ServiceDeployUpdateConfig{}
	}

	return *value
}

func rollbackConfigDiff(left, right *compose.ServiceDeployRollbackConfig) *diff.RollbackConfigDiff {
	leftValue, rightValue := rollbackConfigValue(left), rollbackConfigValue(right)
	result := &diff.RollbackConfigDiff{
		Parallelism:     optionalScalarDiff(leftValue.Parallelism, rightValue.Parallelism),
		Delay:           scalarDiff(leftValue.Delay, rightValue.Delay),
		FailureAction:   scalarDiff(leftValue.FailureAction, rightValue.FailureAction),
		Monitor:         scalarDiff(leftValue.Monitor, rightValue.Monitor),
		MaxFailureRatio: optionalScalarDiff(leftValue.MaxFailureRatio, rightValue.MaxFailureRatio),
		Order:           scalarDiff(leftValue.Order, rightValue.Order),
	}
	if result.Parallelism == nil && result.Delay == nil && result.FailureAction == nil &&
		result.Monitor == nil && result.MaxFailureRatio == nil && result.Order == nil {
		return nil
	}

	return result
}

func rollbackConfigValue(value *compose.ServiceDeployRollbackConfig) compose.ServiceDeployRollbackConfig {
	if value == nil {
		return compose.ServiceDeployRollbackConfig{}
	}

	return *value
}
