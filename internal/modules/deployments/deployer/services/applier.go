// Package services applies final converted service specifications through internal/swarm.
package services

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	// RetryFailedUpdates forces a new task generation only for services with terminal update failures.
	RetryFailedUpdates bool
}

// Apply reconciles services in deterministic name order and returns changed service names.
func (a *Applier) Apply(
	ctx context.Context, stackName string, specs []dockerswarm.ServiceSpec, options ApplyOptions,
) ([]string, error) {
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
	changed := make([]string, 0, len(desired))
	for _, spec := range desired {
		serviceName, serviceChanged, err := a.apply(ctx, stackName, spec, retryFailed)
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
	retryFailed bool,
) (string, bool, error) {
	displayName := serviceName(stackName, desired.Name)
	current, created, err := a.inspectOrCreate(ctx, stackName, displayName, desired)
	if err != nil || created {
		return displayName, created, err
	}
	if serviceSpecsEqual(current.Spec, desired) && (!retryFailed || !failedUpdate(current)) {
		return displayName, false, nil
	}
	err = a.update(ctx, stackName, displayName, desired, current, retryFailed)
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
	retryFailed bool,
) error {
	for attempt := 0; attempt < serviceUpdateAttempts; attempt++ {
		// Rebase runtime-owned values on every optimistic retry.
		applied := cloneSpec(desired)
		applied.TaskTemplate.ForceUpdate = current.Spec.TaskTemplate.ForceUpdate
		if retryFailed && failedUpdate(current) {
			applied.TaskTemplate.ForceUpdate++
		}
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
		if serviceSpecsEqual(current.Spec, desired) && (!retryFailed || !failedUpdate(current)) {
			return nil
		}
	}
	return fmt.Errorf(
		"update service %s: version conflict after %d attempts",
		displayName,
		serviceUpdateAttempts,
	)
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
