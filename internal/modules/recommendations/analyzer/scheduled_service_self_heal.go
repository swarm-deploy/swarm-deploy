package analyzer

import (
	"context"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

// ScheduledServiceSelfHealAnalyzer recommends disabling self-heal for externally scheduled services.
type ScheduledServiceSelfHealAnalyzer struct {
	cfg *config.Config
	now func() time.Time
}

// NewScheduledServiceSelfHealAnalyzer creates an analyzer using current runtime configuration.
func NewScheduledServiceSelfHealAnalyzer(cfg *config.Config) *ScheduledServiceSelfHealAnalyzer {
	return &ScheduledServiceSelfHealAnalyzer{cfg: cfg, now: time.Now}
}

// Analyze checks scheduled services against service, stack, and global self-heal policies.
func (a *ScheduledServiceSelfHealAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	selfHeal := a.cfg.Spec.Sync.Policy.SelfHeal
	for _, configured := range a.cfg.Stacks() {
		if configured.Name == stack.Name && configured.Sync.Policy.SelfHeal != nil {
			selfHeal = *configured.Sync.Policy.SelfHeal
			break
		}
	}

	recs := make([]model.Recommendation, 0)
	for _, service := range stack.Definition.Compose.Services {
		if !isSwarmCronjobService(service) {
			continue
		}
		enabled := selfHeal
		policy, specified, err := service.SelfHealPolicy()
		if err != nil {
			// Invalid service policies are rejected by Compose validation.
			continue
		}
		if specified {
			enabled = policy
		}
		managed, ownershipSpecified, err := service.ReplicasManagedPolicy()
		if err != nil {
			continue
		}
		var changes []string
		if enabled {
			changes = append(changes, labelsdict.ServiceSyncPolicySelfHealLabelKey+`: "false"`)
		}
		if !ownershipSpecified || managed {
			changes = append(changes, labelsdict.ServiceSyncPolicyReplicasManagedLabelKey+`: "false"`)
		}
		if len(changes) == 0 {
			continue
		}

		recs = append(recs, model.Recommendation{
			Type:     model.TypeServiceSelfHealScheduledService,
			Severity: model.SeverityMedium,
			Source:   model.SourceFromStack(stack),
			Subject:  model.Subject{Stack: stack.Name, Service: service.Name},
			Title:    "Disable self-healing for scheduled service",
			Recommendation: "This service is managed by an external scheduler. Automatic self-healing may interfere " +
				"with scheduled executions. Replica management during normal deployments can also conflict with the scheduler. " +
				"Both policies should be disabled for externally scheduled services. Set the remaining deploy labels: " +
				strings.Join(changes, "; ") + ".",
			CreatedAt: a.now(),
		})
	}

	return recs
}
