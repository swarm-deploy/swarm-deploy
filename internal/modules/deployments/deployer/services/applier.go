// Package services applies final converted service specifications through internal/swarm.
package services

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	dockerregistry "github.com/docker/docker/api/types/registry"
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
	// Auth acknowledgements are process-local and contain only fingerprints of successful writes.
	authMu sync.Mutex
	auth   map[string][sha256.Size]byte
}

// New creates a native Docker service applier.
func New(manager swarm.ServiceManager, authManager registry.AuthManager) *Applier {
	return &Applier{manager: manager, authManager: authManager, auth: make(map[string][sha256.Size]byte)}
}

// ApplyOptions controls recovery when a deployment is retried.
type ApplyOptions struct {
	// RetryFailedUpdates restarts terminal failed updates and non-restarting jobs only when replicas are managed.
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
	return result, blockedApplyError(blocked)
}

// RefreshRegistryAuth maintains credentials only for existing services matching expected specs.
// It never creates a service, repairs drift, starts a rollout, or resolves an image.
func (a *Applier) RefreshRegistryAuth(
	ctx context.Context, stackName string, specs []dockerswarm.ServiceSpec,
) error {
	expected := slices.Clone(specs)
	slices.SortFunc(expected, func(left, right dockerswarm.ServiceSpec) int {
		return strings.Compare(left.Name, right.Name)
	})
	for _, spec := range expected {
		if err := a.refreshRegistryAuth(ctx, stackName, spec); err != nil {
			return err
		}
	}
	return nil
}

func (a *Applier) refreshRegistryAuth(ctx context.Context, stackName string, expected dockerswarm.ServiceSpec) error {
	name := serviceName(stackName, expected.Name)
	for range serviceUpdateAttempts {
		current, err := a.manager.Inspect(ctx, expected.Name)
		if errors.Is(err, swarm.ErrServiceNotFound) {
			return nil // Missing services remain the responsibility of deployment and drift remediation.
		}
		if err != nil {
			return fmt.Errorf("inspect service %s for registry authentication: %w", name, err)
		}
		if err = validateOwnership(stackName, expected.Name, current); err != nil {
			return err
		}
		if !registryAuthRefreshAllowed(current, expected) {
			return nil
		}
		options, auth, err := a.updateOptions(current.ID, current.Spec)
		if err != nil {
			return fmt.Errorf("service %s registry authentication: %w", name, err)
		}
		if !a.authRefreshRequired(current.ID, auth) {
			return nil
		}
		err = a.manager.Update(ctx, current.ID, current.Version, cloneSpec(current.Spec), options)
		if err == nil {
			a.rememberAuth(current.ID, auth)
			return nil
		}
		if !errors.Is(err, swarm.ErrServiceConflict) {
			return fmt.Errorf("refresh service %s registry authentication: %w", name, err)
		}
	}
	return fmt.Errorf("refresh service %s registry authentication: version conflict after %d attempts",
		name, serviceUpdateAttempts)
}

func registryAuthRefreshAllowed(current dockerswarm.Service, expected dockerswarm.ServiceSpec) bool {
	if current.UpdateStatus != nil && current.UpdateStatus.State != dockerswarm.UpdateStateCompleted {
		return false
	}
	return authOnlyUpdateSafe(current.Spec) && SpecsEqual(current.Spec, expected)
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
	required, err := a.updateRequired(ctx, displayName, desired, current, retryFailed && managed)
	if err != nil || !required {
		return displayName, false, err
	}
	err = a.update(ctx, stackName, displayName, desired, current, retryFailed && managed, generations)
	if err != nil {
		return displayName, false, err
	}
	return displayName, true, nil
}

func (a *Applier) updateRequired(
	ctx context.Context, displayName string, desired dockerswarm.ServiceSpec,
	current dockerswarm.Service, retryFailed bool,
) (bool, error) {
	if !SpecsEqual(current.Spec, desired) {
		return true, nil
	}
	retry, err := a.retryRequired(ctx, current, retryFailed)
	if err != nil || retry {
		return retry, err
	}
	if !authOnlyUpdateSafe(current.Spec) {
		return false, nil
	}
	auth, err := a.resolveAuth(desired)
	if err != nil {
		return false, fmt.Errorf("service %s registry authentication: %w", displayName, err)
	}
	return a.authRefreshRequired(current.ID, auth), nil
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
			a.rememberAuth(id, options.EncodedRegistryAuth)
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
		refreshed, done, err := a.updateAttempt(
			ctx, stackName, displayName, desired, current, retryFailed, generations,
		)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		current = refreshed
	}
	return fmt.Errorf(
		"update service %s: version conflict after %d attempts",
		displayName,
		serviceUpdateAttempts,
	)
}

func (a *Applier) updateAttempt(
	ctx context.Context,
	stackName string,
	displayName string,
	desired dockerswarm.ServiceSpec,
	current dockerswarm.Service,
	retryFailed bool,
	generations map[string]UpdateGeneration,
) (dockerswarm.Service, bool, error) {
	applied, reused, err := a.prepareUpdate(ctx, displayName, desired, current, retryFailed)
	if errors.Is(err, errSafetyInspectionChanged) {
		return a.refreshConcurrentUpdate(
			ctx, stackName, displayName, desired, current.UpdatedAt, retryFailed, generations,
			"external execution inspection",
		)
	}
	if err != nil {
		return current, false, err
	}
	options, auth, err := a.updateOptions(current.ID, applied)
	if err != nil {
		return current, false, fmt.Errorf("service %s registry authentication: %w", displayName, err)
	}
	options.QueryRegistry = !reused
	err = a.manager.Update(ctx, current.ID, current.Version, applied, options)
	if err == nil {
		a.rememberAuth(current.ID, auth)
		recordExternalUpdate(generations, displayName, applied, current)
		return current, true, nil
	}
	if !errors.Is(err, swarm.ErrServiceConflict) {
		return current, false, fmt.Errorf("update service %s: %w", displayName, err)
	}
	return a.refreshConcurrentUpdate(
		ctx, stackName, displayName, desired, current.UpdatedAt, retryFailed, generations, "version conflict",
	)
}

func (a *Applier) refreshConcurrentUpdate(
	ctx context.Context,
	stackName string,
	displayName string,
	desired dockerswarm.ServiceSpec,
	after time.Time,
	retryFailed bool,
	generations map[string]UpdateGeneration,
	reason string,
) (dockerswarm.Service, bool, error) {
	current, err := a.manager.Inspect(ctx, desired.Name)
	if err != nil {
		return current, false, fmt.Errorf("reinspect service %s after %s: %w", displayName, reason, err)
	}
	if err = validateOwnership(stackName, desired.Name, current); err != nil {
		return current, false, err
	}
	retry, err := a.retryRequired(ctx, current, retryFailed)
	if err != nil {
		return current, false, err
	}
	authPending := false
	if authOnlyUpdateSafe(current.Spec) {
		auth, authErr := a.resolveAuth(desired)
		if authErr != nil {
			return current, false, fmt.Errorf("service %s registry authentication: %w", displayName, authErr)
		}
		authPending = a.authRefreshRequired(current.ID, auth)
	}
	if SpecsEqual(current.Spec, desired) && !retry && !authPending {
		// A concurrent writer already accepted the expected managed fields.
		current.UpdatedAt = after
		recordExternalUpdate(generations, displayName, current.Spec, current)
		return current, true, nil
	}
	return current, false, nil
}

func (a *Applier) prepareUpdate(
	ctx context.Context, name string, desired dockerswarm.ServiceSpec, current dockerswarm.Service, retryFailed bool,
) (dockerswarm.ServiceSpec, bool, error) {
	// Rebase runtime-owned values on every optimistic retry.
	applied := cloneSpec(desired)
	applied.TaskTemplate.ForceUpdate = current.Spec.TaskTemplate.ForceUpdate
	managed, _ := ReplicasManaged(desired) // Validated before any Docker mutation.
	retry, err := a.retryRequired(ctx, current, retryFailed && managed)
	if err != nil {
		return applied, false, err
	}
	if retry {
		applied.TaskTemplate.ForceUpdate++
	} else if SpecsEqual(current.Spec, desired) {
		// Credential refresh preserves the exact task template and runtime-owned fields.
		// Resolving an unchanged tag here could replace otherwise healthy tasks.
		return cloneSpec(current.Spec), true, nil
	}
	reused := normalizeImageResolution(&current.Spec, &applied)
	if !managed {
		preserveReplicas(current.Spec, &applied)
		if err = a.checkUpdateSafety(ctx, name, current, &applied); err != nil {
			return applied, false, err
		}
	}
	return applied, reused, nil
}

func failedUpdate(service dockerswarm.Service) bool {
	if service.UpdateStatus == nil {
		return false
	}
	switch service.UpdateStatus.State {
	case swarm.ServiceUpdateStateFailed, swarm.ServiceUpdateStatePaused, swarm.ServiceUpdateStateRollbackStarted,
		swarm.ServiceUpdateStateRollbackPaused, swarm.ServiceUpdateStateRollbackCompleted:
		return true
	case swarm.ServiceUpdateStateUpdating, swarm.ServiceUpdateStateCompleted:
		return false
	default:
		return false
	}
}

func (a *Applier) createOptions(spec dockerswarm.ServiceSpec) (dockerswarm.ServiceCreateOptions, error) {
	auth, err := a.resolveAuth(spec)
	return dockerswarm.ServiceCreateOptions{EncodedRegistryAuth: auth, QueryRegistry: true}, err
}

func (a *Applier) updateOptions(
	serviceID string, spec dockerswarm.ServiceSpec,
) (dockerswarm.ServiceUpdateOptions, string, error) {
	auth, err := a.resolveAuth(spec)
	if err != nil {
		return dockerswarm.ServiceUpdateOptions{}, "", err
	}
	encoded := auth
	if auth == "" && a.authRefreshRequired(serviceID, auth) {
		// An empty header with RegistryAuthFromSpec preserves Engine's old credentials.
		// A nonempty encoding of anonymous auth replaces a known removed credential.
		encoded, err = dockerregistry.EncodeAuthConfig(dockerregistry.AuthConfig{})
	}
	return dockerswarm.ServiceUpdateOptions{
		EncodedRegistryAuth: encoded, RegistryAuthFrom: dockerswarm.RegistryAuthFromSpec,
	}, auth, err
}

func (a *Applier) resolveAuth(spec dockerswarm.ServiceSpec) (string, error) {
	if spec.TaskTemplate.ContainerSpec == nil {
		return "", nil
	}
	return a.authManager.ResolveImage(spec.TaskTemplate.ContainerSpec.Image)
}

// authOnlyUpdateSafe excludes writes that Swarm can treat as a new job execution.
// Live task inspection cannot remove the race with completion or retry exhaustion.
func authOnlyUpdateSafe(spec dockerswarm.ServiceSpec) bool {
	if spec.Mode.ReplicatedJob != nil || spec.Mode.GlobalJob != nil {
		return false // Every native-job ServiceUpdate advances JobIteration.
	}
	if replicas, known := currentReplicas(spec); known && replicas == 0 {
		return true
	}
	policy := spec.TaskTemplate.RestartPolicy
	if policy == nil {
		return true
	}
	if policy.MaxAttempts != nil && *policy.MaxAttempts > 0 {
		return false
	}
	return policy.Condition == "" || policy.Condition == dockerswarm.RestartPolicyConditionAny
}

// authRefreshRequired forces one write for unknown nonempty credentials after restart.
// Unknown anonymous state retains Docker's RegistryAuthFromSpec behavior; a known
// credential removal is sent explicitly. Engine does not expose its stored pull auth.
func (a *Applier) authRefreshRequired(serviceID, auth string) bool {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	previous, known := a.auth[serviceID]
	return (known || auth != "") && (!known || previous != sha256.Sum256([]byte(auth)))
}

func (a *Applier) rememberAuth(serviceID, auth string) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	a.auth[serviceID] = sha256.Sum256([]byte(auth))
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
