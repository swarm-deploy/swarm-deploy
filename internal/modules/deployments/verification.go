package deployments

import (
	"context"
	"slices"

	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
)

// RecordUpdateGeneration persists the write boundary before Docker can accept an
// update. The returned callback restores the prior obligation after a definite
// Docker rejection; uncertain outcomes retain the intent for crash recovery.
func (s *Service) RecordUpdateGeneration(
	ctx context.Context, attemptID, name string, generation deploymentmodel.UpdateGeneration,
) (func(context.Context) error, error) {
	var previous deploymentmodel.UpdateGeneration
	var unresolved bool
	recordErr := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		deployment, err := s.verificationDeployment(ctx, attemptID)
		if err != nil {
			return err
		}
		previous = deployment.Verification.Generations[name]
		unresolved = slices.Contains(deployment.Verification.UnresolvedServices, name)
		if deployment.Verification.Generations == nil {
			deployment.Verification.Generations = map[string]deploymentmodel.UpdateGeneration{}
		}
		deployment.Verification.Generations[name] = generation
		if !unresolved {
			deployment.Verification.UnresolvedServices = append(deployment.Verification.UnresolvedServices, name)
		}
		return s.store.SaveVerification(ctx, deployment)
	})
	if recordErr != nil {
		return nil, recordErr
	}
	return func(ctx context.Context) error {
		return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
			deployment, err := s.verificationDeployment(ctx, attemptID)
			if err != nil {
				return err
			}
			if deployment.Verification.Generations[name] != generation {
				return deploymentstore.ErrNotRunning
			}
			delete(deployment.Verification.Generations, name)
			if previous.ServiceID != "" {
				deployment.Verification.Generations[name] = previous
			}
			if !unresolved {
				deployment.Verification.UnresolvedServices = slices.DeleteFunc(deployment.Verification.UnresolvedServices,
					func(value string) bool { return value == name })
			}
			return s.store.SaveVerification(ctx, deployment)
		})
	}, nil
}

func (s *Service) verificationDeployment(ctx context.Context, attemptID string) (deploymentmodel.Deployment, error) {
	attempt, err := s.store.GetAttempt(ctx, attemptID)
	if err != nil {
		return deploymentmodel.Deployment{}, err
	}
	deployment, err := s.store.Get(ctx, attempt.DeploymentID)
	if err != nil {
		return deployment, err
	}
	if attempt.Status != deploymentmodel.Running || deployment.LatestAttemptID != attemptID {
		return deployment, deploymentstore.ErrNotRunning
	}
	return deployment, nil
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
