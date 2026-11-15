package deployments

import (
	"context"
	"errors"
	"slices"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
)

func normalizedServiceNames(names []string) []string {
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
	revisions := appliedRevisions(attempts, deploymentmodel.Prepared{ServiceDigests: desired.ServiceDigests})
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
	attempt.ServiceRevisions = make(map[string]deploymentmodel.ServiceRevision, len(options.TargetServices))
	for _, name := range options.TargetServices {
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
func targetsApplied(attempts []deploymentmodel.Attempt, desired deploymentmodel.Prepared, targets []string) bool {
	revisions := appliedRevisions(attempts, desired)
	for _, name := range targets {
		if _, ok := revisions[name]; !ok {
			return false
		}
	}
	return true
}

func appliedRevisions(
	attempts []deploymentmodel.Attempt, desired deploymentmodel.Prepared,
) map[string]deploymentmodel.ServiceRevision {
	revisions := make(map[string]deploymentmodel.ServiceRevision)
	for name, digest := range desired.ServiceDigests {
		for _, attempt := range attempts {
			if !attempt.Targets(name) {
				continue
			}
			revision := attempt.ServiceRevisions[name]
			if attempt.Status == deploymentmodel.Succeeded && revision.Digest == digest {
				revisions[name] = revision
			}
			break
		}
	}
	return revisions
}
