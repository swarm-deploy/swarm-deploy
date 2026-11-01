// Package services applies final converted service specifications through internal/swarm.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	serviceUpdateAttempts  = 3
	stackImageLabel        = "com.docker.stack.image"
	defaultStopGracePeriod = 10 * time.Second
	defaultRestartDelay    = 5 * time.Second
	defaultUpdateMonitor   = 5 * time.Second
)

// Applier creates and updates final Docker service specifications.
type Applier struct {
	manager     swarm.ServiceManager
	authManager registry.AuthManager
}

// New creates a native Docker service applier.
func New(manager swarm.ServiceManager, authManager registry.AuthManager) *Applier {
	return &Applier{manager: manager, authManager: authManager}
}

// Apply reconciles services in deterministic name order and returns changed service names.
func (a *Applier) Apply(ctx context.Context, stackName string, specs []dockerswarm.ServiceSpec) ([]string, error) {
	desired := slices.Clone(specs)
	slices.SortFunc(desired, func(left, right dockerswarm.ServiceSpec) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})

	changed := make([]string, 0, len(desired))
	for _, spec := range desired {
		serviceName, serviceChanged, err := a.apply(ctx, stackName, spec)
		if err != nil {
			return changed, err
		}
		if serviceChanged {
			changed = append(changed, serviceName)
		}
	}
	return changed, nil
}

func (a *Applier) apply(
	ctx context.Context, stackName string, desired dockerswarm.ServiceSpec,
) (string, bool, error) {
	displayName := serviceName(stackName, desired.Name)
	current, created, err := a.inspectOrCreate(ctx, stackName, displayName, desired)
	if err != nil || created {
		return displayName, created, err
	}
	if serviceSpecsEqual(current.Spec, desired) {
		return displayName, false, nil
	}
	err = a.update(ctx, stackName, displayName, desired, current)
	if err != nil {
		return displayName, false, err
	}
	return displayName, true, nil
}

func (a *Applier) inspectOrCreate(
	ctx context.Context,
	stackName string,
	displayName string,
	desired dockerswarm.ServiceSpec,
) (dockerswarm.Service, bool, error) {
	current, err := a.manager.Inspect(ctx, desired.Name)
	if errors.Is(err, swarm.ErrServiceNotFound) {
		options, optionsErr := a.createOptions(desired)
		if optionsErr != nil {
			return current, false, fmt.Errorf("service %s registry authentication: %w", displayName, optionsErr)
		}
		if _, createErr := a.manager.Create(ctx, desired, options); createErr == nil {
			return dockerswarm.Service{}, true, nil
		} else if !errors.Is(createErr, swarm.ErrServiceConflict) {
			return current, false, fmt.Errorf("create service %s: %w", displayName, createErr)
		}
		current, err = a.manager.Inspect(ctx, desired.Name)
	}
	if err != nil {
		return current, false, fmt.Errorf("inspect service %s: %w", displayName, err)
	}
	if err = validateOwnership(stackName, desired.Name, current); err != nil {
		return current, false, err
	}
	return current, false, nil
}

func (a *Applier) update(
	ctx context.Context,
	stackName string,
	displayName string,
	desired dockerswarm.ServiceSpec,
	current dockerswarm.Service,
) error {
	for attempt := 0; attempt < serviceUpdateAttempts; attempt++ {
		// Rebase runtime-owned values on every optimistic retry.
		applied := cloneSpec(desired)
		applied.TaskTemplate.ForceUpdate = current.Spec.TaskTemplate.ForceUpdate
		reused := normalizeImageResolution(&current.Spec, &applied)
		options, err := a.updateOptions(applied)
		if err != nil {
			return fmt.Errorf("service %s registry authentication: %w", displayName, err)
		}
		options.QueryRegistry = !reused && !digestPinned(applied)
		err = a.manager.Update(ctx, current.ID, current.Version, applied, options)
		if err == nil {
			return nil
		}
		if !errors.Is(err, swarm.ErrServiceConflict) {
			return fmt.Errorf("update service %s: %w", displayName, err)
		}
		current, err = a.manager.Inspect(ctx, desired.Name)
		if err != nil {
			return fmt.Errorf("reinspect service %s after version conflict: %w", displayName, err)
		}
		if err = validateOwnership(stackName, desired.Name, current); err != nil {
			return err
		}
		if serviceSpecsEqual(current.Spec, desired) {
			return nil
		}
	}
	return fmt.Errorf(
		"update service %s: version conflict after %d attempts",
		displayName,
		serviceUpdateAttempts,
	)
}

func (a *Applier) createOptions(spec dockerswarm.ServiceSpec) (dockerswarm.ServiceCreateOptions, error) {
	auth, err := a.resolveAuth(spec)
	return dockerswarm.ServiceCreateOptions{EncodedRegistryAuth: auth, QueryRegistry: !digestPinned(spec)}, err
}

func (a *Applier) updateOptions(spec dockerswarm.ServiceSpec) (dockerswarm.ServiceUpdateOptions, error) {
	auth, err := a.resolveAuth(spec)
	return dockerswarm.ServiceUpdateOptions{
		EncodedRegistryAuth: auth, RegistryAuthFrom: dockerswarm.RegistryAuthFromSpec, QueryRegistry: true,
	}, err
}

func (a *Applier) resolveAuth(spec dockerswarm.ServiceSpec) (string, error) {
	if spec.TaskTemplate.ContainerSpec == nil {
		return "", nil
	}
	return a.authManager.ResolveImage(spec.TaskTemplate.ContainerSpec.Image)
}

func validateOwnership(stackName, desiredName string, current dockerswarm.Service) error {
	if current.ID == "" || current.Spec.Name != desiredName {
		return fmt.Errorf("service %s has incompatible Docker identity", desiredName)
	}
	if current.Spec.Labels[labelsdict.StackNamespace] != stackName {
		return fmt.Errorf("service %s already exists but is not owned by stack %s", desiredName, stackName)
	}
	return nil
}

func serviceName(stackName, fullName string) string {
	prefix := stackName + "_"
	if len(fullName) > len(prefix) && fullName[:len(prefix)] == prefix {
		return fullName[len(prefix):]
	}
	return fullName
}

func serviceSpecsEqual(current, desired dockerswarm.ServiceSpec) bool {
	current = cloneSpec(current)
	desired = cloneSpec(desired)
	desired.TaskTemplate.ForceUpdate = current.TaskTemplate.ForceUpdate
	normalizeImageResolution(&current, &desired)
	normalizeDockerDefaults(&current)
	normalizeDockerDefaults(&desired)
	normalizeServiceSets(&current)
	normalizeServiceSets(&desired)
	return reflect.DeepEqual(canonicalSpec(current), canonicalSpec(desired))
}

func cloneSpec(spec dockerswarm.ServiceSpec) dockerswarm.ServiceSpec {
	payload, _ := json.Marshal(spec)
	var cloned dockerswarm.ServiceSpec
	_ = json.Unmarshal(payload, &cloned)
	return cloned
}

// normalizeImageResolution reuses the resolved image only when the actual reference
// can be the SDK's resolution of the unchanged Compose reference. Labels alone
// are insufficient evidence: repository, tag and explicit digest must agree.
func normalizeImageResolution(current, desired *dockerswarm.ServiceSpec) bool {
	if current.TaskTemplate.ContainerSpec == nil || desired.TaskTemplate.ContainerSpec == nil {
		return false
	}
	wanted := desired.TaskTemplate.ContainerSpec.Image
	actual := current.TaskTemplate.ContainerSpec.Image
	if current.Labels[stackImageLabel] == "" || current.Labels[stackImageLabel] != desired.Labels[stackImageLabel] ||
		!resolvedImageMatches(actual, wanted) {
		return false
	}
	desired.TaskTemplate.ContainerSpec.Image = actual
	if current.TaskTemplate.Placement != nil && len(current.TaskTemplate.Placement.Platforms) > 0 {
		if desired.TaskTemplate.Placement == nil {
			desired.TaskTemplate.Placement = &dockerswarm.Placement{}
		}
		desired.TaskTemplate.Placement.Platforms = slices.Clone(current.TaskTemplate.Placement.Platforms)
	}
	return true
}

func resolvedImageMatches(actual, wanted string) bool {
	current, err := reference.ParseNormalizedNamed(actual)
	if err != nil {
		return false
	}
	desired, err := reference.ParseNormalizedNamed(wanted)
	if err != nil || current.Name() != desired.Name() {
		return false
	}
	if pinned, ok := desired.(reference.Canonical); ok {
		resolved, canonical := current.(reference.Canonical)
		return canonical && pinned.Digest() == resolved.Digest()
	}
	desired = reference.TagNameOnly(desired)
	current = reference.TagNameOnly(current)
	desiredTag, tagged := desired.(reference.Tagged)
	currentTag, currentTagged := current.(reference.Tagged)
	return tagged && currentTagged && desiredTag.Tag() == currentTag.Tag()
}

func digestPinned(spec dockerswarm.ServiceSpec) bool {
	if spec.TaskTemplate.ContainerSpec == nil {
		return false
	}
	named, err := reference.ParseNormalizedNamed(spec.TaskTemplate.ContainerSpec.Image)
	if err != nil {
		return false
	}
	_, pinned := named.(reference.Canonical)
	return pinned
}

func normalizeDockerDefaults(spec *dockerswarm.ServiceSpec) {
	if job := spec.Mode.ReplicatedJob; job != nil {
		if job.MaxConcurrent == nil {
			value := uint64(1)
			job.MaxConcurrent = &value
		}
		if job.TotalCompletions == nil {
			value := *job.MaxConcurrent
			job.TotalCompletions = &value
		}
	}
	if spec.Mode.Replicated != nil && spec.Mode.Replicated.Replicas == nil {
		replicas := uint64(1)
		spec.Mode.Replicated.Replicas = &replicas
	}
	if spec.EndpointSpec == nil {
		spec.EndpointSpec = &dockerswarm.EndpointSpec{}
	}
	if spec.EndpointSpec.Mode == "" {
		spec.EndpointSpec.Mode = dockerswarm.ResolutionModeVIP
	}
	if spec.TaskTemplate.ContainerSpec != nil {
		// Engine's SwarmKit conversion returns explicit runtime/isolation
		// defaults even when they were omitted in the create request.
		if spec.TaskTemplate.Runtime == "" {
			spec.TaskTemplate.Runtime = dockerswarm.RuntimeContainer
		}
		if spec.TaskTemplate.ContainerSpec.Isolation == "" {
			spec.TaskTemplate.ContainerSpec.Isolation = container.IsolationDefault
		}
		if spec.TaskTemplate.ContainerSpec.StopGracePeriod == nil {
			value := defaultStopGracePeriod
			spec.TaskTemplate.ContainerSpec.StopGracePeriod = &value
		}
	}
	if spec.TaskTemplate.RestartPolicy == nil {
		spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{}
	}
	restart := spec.TaskTemplate.RestartPolicy
	if restart.Condition == "" {
		restart.Condition = dockerswarm.RestartPolicyConditionAny
	}
	if restart.Delay == nil {
		value := defaultRestartDelay
		restart.Delay = &value
	}
	if restart.MaxAttempts == nil {
		value := uint64(0)
		restart.MaxAttempts = &value
	}
	spec.UpdateConfig = normalizeUpdateConfig(spec.UpdateConfig)
	spec.RollbackConfig = normalizeUpdateConfig(spec.RollbackConfig)
}

func normalizeUpdateConfig(config *dockerswarm.UpdateConfig) *dockerswarm.UpdateConfig {
	if config == nil {
		config = &dockerswarm.UpdateConfig{Parallelism: 1}
	}
	if config.FailureAction == "" {
		config.FailureAction = dockerswarm.UpdateFailureActionPause
	}
	if config.Monitor == 0 {
		config.Monitor = defaultUpdateMonitor
	}
	if config.Order == "" {
		config.Order = dockerswarm.UpdateOrderStopFirst
	}
	return config
}

func canonicalSpec(spec dockerswarm.ServiceSpec) any {
	payload, _ := json.Marshal(spec)
	var value any
	_ = json.Unmarshal(payload, &value)
	return pruneEmpty(value)
}

func pruneEmpty(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			child = pruneEmpty(child)
			if child == nil || isEmptyCollection(child) {
				delete(typed, key)
				continue
			}
			typed[key] = child
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = pruneEmpty(typed[index])
		}
		return typed
	default:
		return value
	}
}

func isEmptyCollection(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}
