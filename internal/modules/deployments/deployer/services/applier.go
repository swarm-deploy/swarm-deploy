// Package services applies final converted service specifications through internal/swarm.
package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	serviceUpdateAttempts = 3
	stackImageLabel       = "com.docker.stack.image"
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

// ApplyOptions controls recovery when a deployment is retried.
type ApplyOptions struct {
	// RetryFailedUpdates restarts terminal failed updates only when replicas are managed.
	RetryFailedUpdates bool
}

// UpdateGeneration records the runtime generation preserved by an accepted external service write.
type UpdateGeneration struct {
	// ServiceID identifies the service updated by this apply.
	ServiceID string
	// ForceUpdate is the external execution generation preserved in the accepted spec.
	ForceUpdate uint64
	// After is the last inspected service timestamp before the successful write.
	After time.Time
}

// ApplyResult contains mutations and external generation evidence from this apply.
type ApplyResult struct {
	// ChangedServices lists services created or updated by this apply.
	ChangedServices []string
	// ExternalUpdates records accepted writes, rebased on optimistic retries.
	ExternalUpdates map[string]UpdateGeneration
}

// Apply reconciles services in deterministic name order and returns accepted mutations.
func (a *Applier) Apply(
	ctx context.Context, stackName string, specs []dockerswarm.ServiceSpec, options ApplyOptions,
) (ApplyResult, error) {
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

	retryFailed := options.RetryFailedUpdates
	result := ApplyResult{ChangedServices: make([]string, 0, len(desired)), ExternalUpdates: map[string]UpdateGeneration{}}
	var blocked []error
	for _, spec := range desired {
		serviceName, serviceChanged, err := a.apply(ctx, stackName, spec, retryFailed, result.ExternalUpdates)
		if err != nil {
			if errors.Is(err, ErrUpdateBlocked) {
				blocked = append(blocked, err)
				continue
			}
			return result, errors.Join(append(blocked, err)...)
		}
		if serviceChanged {
			result.ChangedServices = append(result.ChangedServices, serviceName)
		}
	}
	return result, errors.Join(blocked...)
}

func (a *Applier) apply(
	ctx context.Context, stackName string, desired dockerswarm.ServiceSpec,
	retryFailed bool, generations map[string]UpdateGeneration,
) (string, bool, error) {
	displayName := serviceName(stackName, desired.Name)
	managed, err := ReplicasManaged(desired)
	if err != nil {
		return displayName, false, fmt.Errorf("service %s replicas ownership policy: %w", displayName, err)
	}
	current, created, err := a.inspectOrCreate(ctx, stackName, displayName, desired)
	if err != nil || created {
		if created {
			recordExternalUpdate(generations, displayName, desired, current)
		}
		return displayName, created, err
	}
	if SpecsEqual(current.Spec, desired) && (!managed || !retryFailed || !failedUpdate(current)) {
		return displayName, false, nil
	}
	err = a.update(ctx, stackName, displayName, desired, current, retryFailed && managed, generations)
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
		if id, createErr := a.manager.Create(ctx, desired, options); createErr == nil {
			return dockerswarm.Service{ID: id, Spec: desired}, true, nil
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
	retryFailed bool, generations map[string]UpdateGeneration,
) error {
	for attempt := 0; attempt < serviceUpdateAttempts; attempt++ {
		applied, reused, err := a.prepareUpdate(ctx, displayName, desired, current, retryFailed)
		if err != nil {
			return err
		}
		options, err := a.updateOptions(applied)
		if err != nil {
			return fmt.Errorf("service %s registry authentication: %w", displayName, err)
		}
		options.QueryRegistry = !reused && !digestPinned(applied)
		err = a.manager.Update(ctx, current.ID, current.Version, applied, options)
		if err == nil {
			recordExternalUpdate(generations, displayName, applied, current)
			return nil
		}
		if !errors.Is(err, swarm.ErrServiceConflict) {
			return fmt.Errorf("update service %s: %w", displayName, err)
		}
		after := current.UpdatedAt
		current, err = a.manager.Inspect(ctx, desired.Name)
		if err != nil {
			return fmt.Errorf("reinspect service %s after version conflict: %w", displayName, err)
		}
		if err = validateOwnership(stackName, desired.Name, current); err != nil {
			return err
		}
		if SpecsEqual(current.Spec, desired) && (!retryFailed || !failedUpdate(current)) {
			// A concurrent writer already accepted the expected managed fields.
			current.UpdatedAt = after
			recordExternalUpdate(generations, displayName, current.Spec, current)
			return nil
		}
	}
	return fmt.Errorf(
		"update service %s: version conflict after %d attempts",
		displayName,
		serviceUpdateAttempts,
	)
}

func (a *Applier) prepareUpdate(
	ctx context.Context, name string, desired dockerswarm.ServiceSpec, current dockerswarm.Service, retryFailed bool,
) (dockerswarm.ServiceSpec, bool, error) {
	// Rebase runtime-owned values on every optimistic retry.
	applied := cloneSpec(desired)
	applied.TaskTemplate.ForceUpdate = current.Spec.TaskTemplate.ForceUpdate
	managed, _ := ReplicasManaged(desired) // Validated before any Docker mutation.
	if retryFailed && managed && failedUpdate(current) {
		applied.TaskTemplate.ForceUpdate++
	}
	reused := normalizeImageResolution(&current.Spec, &applied)
	if !managed {
		preserveReplicas(current.Spec, &applied)
		if err := a.checkUpdateSafety(ctx, name, current, applied); err != nil {
			return applied, false, err
		}
	}
	return applied, reused, nil
}

func failedUpdate(service dockerswarm.Service) bool {
	if service.UpdateStatus == nil {
		return false
	}
	switch string(service.UpdateStatus.State) {
	case "failed", "paused", "rollback_started", "rollback_paused", "rollback_completed":
		return true
	default:
		return false
	}
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

func recordExternalUpdate(generations map[string]UpdateGeneration, name string,
	spec dockerswarm.ServiceSpec, current dockerswarm.Service,
) {
	managed, _ := ReplicasManaged(spec) // Already validated before Docker mutations.
	if !managed {
		generations[name] = UpdateGeneration{
			ServiceID: current.ID, ForceUpdate: spec.TaskTemplate.ForceUpdate, After: current.UpdatedAt,
		}
	}
}
