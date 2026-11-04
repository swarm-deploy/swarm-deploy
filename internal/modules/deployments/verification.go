package deployments

import (
	"context"
	"slices"

	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
)

// RecordUpdateGeneration persists the write boundary before Docker can accept an
// update. A crash between apply and verification must not lose its generation.
func (s *Service) RecordUpdateGeneration(
	ctx context.Context, attemptID, name string, generation deploymentmodel.UpdateGeneration,
) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		attempt, err := s.store.GetAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		deployment, err := s.store.Get(ctx, attempt.DeploymentID)
		if err != nil {
			return err
		}
		if attempt.Status != deploymentmodel.Running || deployment.LatestAttemptID != attemptID {
			return deploymentstore.ErrNotRunning
		}
		if deployment.Verification.Generations == nil {
			deployment.Verification.Generations = map[string]deploymentmodel.UpdateGeneration{}
		}
		deployment.Verification.Generations[name] = generation
		if !slices.Contains(deployment.Verification.UnresolvedServices, name) {
			deployment.Verification.UnresolvedServices = append(deployment.Verification.UnresolvedServices, name)
		}
		return s.store.SaveVerification(ctx, deployment)
	})
}

func unresolvedGenerations(
	verification deploymentmodel.Verification, names []string,
) map[string]deploymentmodel.UpdateGeneration {
	var result map[string]deploymentmodel.UpdateGeneration
	for _, name := range names {
		if generation, ok := verification.Generations[name]; ok {
			if result == nil {
				result = map[string]deploymentmodel.UpdateGeneration{}
			}
			result[name] = generation
		}
	}
	return result
}
