package deployments

import (
	"context"
	"errors"
	"maps"
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
	snapshot, err := s.store.Snapshot(ctx, deployment.ID)
	if err != nil {
		return false, err
	}
	deployment.Attempts = slices.Clone(deployment.Attempts)
	deployment.Attempts[0] = attempt
	revisions, err := s.appliedRevisions(ctx, deployment, snapshot, nil)
	if err != nil {
		return false, err
	}
	for name, digest := range desired.ServiceDigests {
		if _, ok := revisions[name]; ok {
			continue
		}
		if digest == "" || baseline.ServiceDigests[name] != digest || slices.Contains(baseline.UnconfirmedServices, name) {
			return false, nil
		}
	}
	return len(desired.ServiceDigests) == len(desired.Compose.Services), nil
}

func setAttemptTargets(
	attempt *deploymentmodel.Attempt, desired deploymentmodel.Prepared, commit string, options StartOptions,
) {
	attempt.TargetServices = slices.Clone(options.TargetServices)
	attempt.SkippedServices = slices.Clone(options.SkippedServices)
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

// unappliedTargets skips services with a receipt for this desired snapshot.
func (s *Service) unappliedTargets(
	ctx context.Context, deployment deploymentmodel.Deployment, snapshot deploymentmodel.Prepared, targets []string,
) ([]string, error) {
	if targets == nil {
		targets = snapshot.Definition.Compose.Services.Names()
	}
	revisions, err := s.appliedRevisions(ctx, deployment, snapshot, targets)
	if err != nil {
		return nil, err
	}
	unapplied := []string{}
	for _, name := range targets {
		if _, ok := revisions[name]; !ok {
			unapplied = append(unapplied, name)
		}
	}
	return unapplied, nil
}

// appliedRevisions looks for the latest execution of each target, walking back
// only while consecutive Deployments describe the same desired snapshot.
func (s *Service) appliedRevisions(
	ctx context.Context, deployment deploymentmodel.Deployment, snapshot deploymentmodel.Prepared, targets []string,
) (map[string]deploymentmodel.ServiceRevision, error) {
	if targets == nil {
		targets = snapshot.Definition.Compose.Services.Names()
	}
	unseen := make(map[string]struct{}, len(targets))
	for _, name := range targets {
		unseen[name] = struct{}{}
	}
	revisions := make(map[string]deploymentmodel.ServiceRevision)
	for len(unseen) > 0 {
		collectReceipts(deployment, snapshot, unseen, revisions)
		if len(unseen) == 0 {
			break
		}
		id, previous, err := s.store.PreviousSnapshot(ctx, deployment.Stack, deployment.ID)
		if errors.Is(err, deploymentstore.ErrNotFound) {
			break
		}
		if err != nil {
			return revisions, err
		}
		if previous.Digest != snapshot.Digest || !maps.Equal(previous.ServiceDigests, snapshot.ServiceDigests) {
			break
		}
		deployment, err = s.store.Get(ctx, id)
		if err != nil {
			return revisions, err
		}
		snapshot = previous
	}
	return revisions, nil
}

func collectReceipts(
	deployment deploymentmodel.Deployment, snapshot deploymentmodel.Prepared,
	unseen map[string]struct{}, revisions map[string]deploymentmodel.ServiceRevision,
) {
	attempts := deployment.Attempts
	if len(attempts) == 0 && deployment.Status == deploymentmodel.Succeeded {
		// A legacy successful Deployment represents one whole-stack execution.
		attempts = []deploymentmodel.Attempt{{Status: deploymentmodel.Succeeded}}
	}
	for _, attempt := range attempts {
		for name := range unseen {
			if !attempt.Targets(name) {
				continue
			}
			// A newer execution invalidates older receipts, even when it failed.
			delete(unseen, name)
			if revision, ok := successfulRevision(attempt, snapshot, name, deployment.Commit); ok {
				revisions[name] = revision
			}
		}
	}
}

func successfulRevision(
	attempt deploymentmodel.Attempt, snapshot deploymentmodel.Prepared, name, commit string,
) (deploymentmodel.ServiceRevision, bool) {
	digest := snapshot.ServiceDigests[name]
	if attempt.Status != deploymentmodel.Succeeded || digest == "" {
		return deploymentmodel.ServiceRevision{}, false
	}
	revision, recorded := attempt.ServiceRevisions[name]
	if !recorded && attempt.TargetServices == nil && !slices.Contains(snapshot.UnconfirmedServices, name) {
		revision = deploymentmodel.ServiceRevision{Digest: digest, Commit: commit}
	}
	return revision, revision.Digest == digest
}
