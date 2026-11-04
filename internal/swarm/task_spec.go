package swarm

import (
	"reflect"
	"slices"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

const (
	taskDefaultStopGracePeriod = 10 * time.Second
	taskDefaultRestartDelay    = 5 * time.Second
)

// TaskSpec is a comparable Docker task specification with Engine defaults normalized.
type TaskSpec struct {
	value dockerswarm.TaskSpec
}

// TaskSpecComparison describes whether a task was created from a service template.
type TaskSpecComparison struct {
	mismatchPaths []string
}

// NewTaskSpec wraps an actual Docker task specification for safe comparison.
func NewTaskSpec(spec dockerswarm.TaskSpec) TaskSpec {
	return TaskSpec{value: normalizeTaskSpec(spec)}
}

// CompareServiceTemplate compares an actual task specification with a service task template.
func (s TaskSpec) CompareServiceTemplate(template dockerswarm.TaskSpec) TaskSpecComparison {
	template = normalizeTaskSpec(template)
	if reflect.DeepEqual(s.value, template) {
		return TaskSpecComparison{}
	}

	var paths []string
	taskSpecDifferencePaths(reflect.ValueOf(template), reflect.ValueOf(s.value), "TaskTemplate", &paths)
	return TaskSpecComparison{mismatchPaths: paths}
}

// Equal reports whether the normalized task and service template are equivalent.
func (c TaskSpecComparison) Equal() bool {
	return len(c.mismatchPaths) == 0
}

// MismatchPaths returns Docker field paths without any specification values.
func (c TaskSpecComparison) MismatchPaths() []string {
	return slices.Clone(c.mismatchPaths)
}

// normalizeTaskSpec covers only defaults proven by Docker's Engine conversion and
// SwarmKit service interpolation. Nil and empty collections elsewhere remain strict.
func normalizeTaskSpec(spec dockerswarm.TaskSpec) dockerswarm.TaskSpec {
	if spec.Runtime == "" {
		switch {
		case spec.ContainerSpec != nil && spec.PluginSpec == nil:
			spec.Runtime = dockerswarm.RuntimeContainer
		case spec.PluginSpec != nil && spec.ContainerSpec == nil:
			spec.Runtime = dockerswarm.RuntimePlugin
		}
	}
	if spec.ContainerSpec != nil {
		containerSpec := *spec.ContainerSpec
		if containerSpec.Isolation == "" {
			containerSpec.Isolation = container.IsolationDefault
		}
		if containerSpec.StopGracePeriod == nil {
			value := taskDefaultStopGracePeriod
			containerSpec.StopGracePeriod = &value
		}
		if containerSpec.DNSConfig == nil {
			containerSpec.DNSConfig = &dockerswarm.DNSConfig{}
		}
		spec.ContainerSpec = &containerSpec
	}
	if spec.Resources == nil {
		spec.Resources = &dockerswarm.ResourceRequirements{}
	}
	if spec.RestartPolicy == nil {
		spec.RestartPolicy = &dockerswarm.RestartPolicy{}
	} else {
		restartPolicy := *spec.RestartPolicy
		spec.RestartPolicy = &restartPolicy
	}
	if spec.RestartPolicy.Condition == "" {
		spec.RestartPolicy.Condition = dockerswarm.RestartPolicyConditionAny
	}
	if spec.RestartPolicy.Delay == nil {
		value := taskDefaultRestartDelay
		spec.RestartPolicy.Delay = &value
	}
	if spec.RestartPolicy.MaxAttempts == nil {
		value := uint64(0)
		spec.RestartPolicy.MaxAttempts = &value
	}
	if spec.Placement == nil {
		spec.Placement = &dockerswarm.Placement{}
	}
	return spec
}

func taskSpecDifferencePaths(left, right reflect.Value, path string, paths *[]string) {
	if !left.IsValid() || !right.IsValid() || left.Type() != right.Type() {
		*paths = append(*paths, path)
		return
	}
	if reflect.DeepEqual(left.Interface(), right.Interface()) {
		return
	}

	switch left.Kind() { //nolint:exhaustive // Scalar and collection kinds share the safe field-only fallback.
	case reflect.Interface, reflect.Pointer:
		if left.IsNil() || right.IsNil() {
			*paths = append(*paths, path)
			return
		}
		taskSpecDifferencePaths(left.Elem(), right.Elem(), path, paths)
	case reflect.Struct:
		before := len(*paths)
		for i := 0; i < left.NumField(); i++ {
			field := left.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			taskSpecDifferencePaths(left.Field(i), right.Field(i), path+"."+field.Name, paths)
		}
		if len(*paths) == before {
			*paths = append(*paths, path)
		}
	default:
		// Slices and maps stop at their field name so indexes, map keys, and
		// potentially sensitive values never enter diagnostics.
		*paths = append(*paths, path)
	}
}
