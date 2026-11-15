package deployments

import (
	"context"
	"errors"
	"slices"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
)

// normalizedServiceNames keeps nil distinct from an empty scope.
func normalizedServiceNames(names []string) []string {
	if names == nil {
		return nil
	}
	result := append([]string{}, names...)
	slices.Sort(result)
	return slices.Compact(result)
}

// A partial success can advance the full-stack baseline only when unchanged
// services are covered by the previous baseline or successful attempts.
func (s *Service) coversDesired(
	ctx context.Context, deployment deploymentmodel.Deployment,
	attempt deploymentmodel.Attempt, desired compose.File,
) (bool, error) {
	if len(deployment.Verification.UnresolvedServices) > 0 {
		return false, nil
	}
	if len(attempt.Services(desired)) == len(desired.Compose.Services) {
		return true, nil
	}
	baseline, err := s.store.Baseline(ctx, deployment.Stack)
	if err != nil && !errors.Is(err, deploymentstore.ErrNotFound) {
		return false, err
	}
	attempts := append([]deploymentmodel.Attempt{attempt}, deployment.Attempts...)
	revisions := appliedRevisions(attempts, deploymentmodel.Prepared{ServiceDigests: desired.ServiceDigests}, deployment.Commit)
	for name, digest := range desired.ServiceDigests {
		if _, ok := revisions[name]; !ok && (digest == "" || baseline.ServiceDigests[name] != digest) {
			return false, nil
		}
	}
	return len(desired.ServiceDigests) == len(desired.Compose.Services), nil
}

func setAttemptTargets(
	attempt *deploymentmodel.Attempt, desired deploymentmodel.Prepared, commit string, options StartOptions,
) {
	attempt.TargetServices = slices.Clone(options.TargetServices)
	names := options.TargetServices
	if names == nil {
		names = desired.Definition.Compose.Services.Names()
	}
	attempt.ServiceRevisions = make(map[string]deploymentmodel.ServiceRevision, len(names))
	for _, name := range names {
		serviceCommit := options.ServiceCommits[name]
		if serviceCommit == "" {
			serviceCommit = commit
		}
		attempt.ServiceRevisions[name] = deploymentmodel.ServiceRevision{
			Digest: desired.ServiceDigests[name], Commit: serviceCommit,
		}
	}
}

// targetsApplied checks immutable execution receipts, not the caller's policy or its projection timing.
// A nil target list selects every desired service; an empty list selects none.
func targetsApplied(deployment deploymentmodel.Deployment, desired deploymentmodel.Prepared, targets []string) bool {
	if targets == nil {
		targets = desired.Definition.Compose.Services.Names()
	}
	revisions := appliedRevisions(receipts(deployment), desired, deployment.Commit)
	for _, name := range targets {
		if _, ok := revisions[name]; !ok {
			return false
		}
	}
	return true
}

// receipts returns a Deployment's Attempts as execution receipts. A successful
// Deployment recorded before Attempts were persisted counts as one whole-stack Attempt.
func receipts(deployment deploymentmodel.Deployment) []deploymentmodel.Attempt {
	if len(deployment.Attempts) == 0 && deployment.Status == deploymentmodel.Succeeded {
		return []deploymentmodel.Attempt{{Status: deploymentmodel.Succeeded}}
	}
	return deployment.Attempts
}

// appliedRevisions resolves the newest receipt of each service in the snapshot
// described by desired. A historical whole-stack Attempt has no per-service receipt:
// it applied the whole snapshot of its Deployment at the Deployment commit.
func appliedRevisions(
	attempts []deploymentmodel.Attempt, desired deploymentmodel.Prepared, commit string,
) map[string]deploymentmodel.ServiceRevision {
	revisions := make(map[string]deploymentmodel.ServiceRevision)
	for name, digest := range desired.ServiceDigests {
		for _, attempt := range attempts {
			if !attempt.Targets(name) {
				continue
			}
			revision, recorded := attempt.ServiceRevisions[name]
			if !recorded && attempt.TargetServices == nil {
				revision = deploymentmodel.ServiceRevision{Digest: digest, Commit: commit}
			}
			if attempt.Status == deploymentmodel.Succeeded && revision.Digest == digest {
				revisions[name] = revision
			}
			break
		}
	}
	return revisions
}

// consecutiveFailures counts the newest failed or interrupted Attempts. A targeted
// Attempt can follow a success of the same desired state, so success resets backoff.
func consecutiveFailures(deployment deploymentmodel.Deployment) int {
	if len(deployment.Attempts) == 0 {
		return deployment.AttemptCount
	}
	count := 0
	for _, attempt := range deployment.Attempts {
		if !failedAttempt(attempt) {
			break
		}
		count++
	}
	return count
}

// retriesFailedAttempt reports whether an Attempt follows a failed or interrupted one
// rather than a success that left other targets of the same desired state unapplied.
func retriesFailedAttempt(deployment deploymentmodel.Deployment, attempt deploymentmodel.Attempt) bool {
	for _, previous := range deployment.Attempts {
		if previous.Number < attempt.Number {
			return failedAttempt(previous)
		}
	}
	return attempt.Number > 1
}

func failedAttempt(attempt deploymentmodel.Attempt) bool {
	return attempt.Status == deploymentmodel.Failed || attempt.Status == deploymentmodel.Interrupted
}
