package deployer

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	resourcepkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type resolvedResources struct {
	configs map[string]ResolvedResource
	secrets map[string]ResolvedResource
}

// resourceReconciler is a narrow compatibility adapter around the extracted package.
type resourceReconciler struct {
	configs swarm.ConfigManager
	secrets swarm.SecretManager
	inner   *resourcepkg.Reconciler
}

func newResourceReconciler(swarmService *swarm.Swarm) *resourceReconciler {
	return &resourceReconciler{inner: resourcepkg.New(swarmService)}
}

func (r *resourceReconciler) Reconcile(
	ctx context.Context,
	stackName string,
	configs compose.Configs,
	secrets compose.Secrets,
	secretData map[string][]byte,
) (resolvedResources, error) {
	reconciler := r.inner
	if reconciler == nil {
		reconciler = resourcepkg.NewWithManagers(r.configs, r.secrets)
	}
	resolved, err := reconciler.Reconcile(ctx, stackName, configs, secrets, secretData)
	if err != nil {
		return resolvedResources{}, err
	}
	return resolvedResources{configs: resolved.Configs, secrets: resolved.Secrets}, nil
}
