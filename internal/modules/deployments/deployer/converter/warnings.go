package converter

import (
	"context"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// WarnIgnoredOptions logs compatibility warnings once at the pipeline's validation step.
// Validate, PlanNetworks and Convert stay side-effect free and do not repeat warnings.
func (*Converter) WarnIgnoredOptions(ctx context.Context, stackName string, desired compose.Compose) {
	for _, service := range desired.Services {
		for _, option := range service.IgnoredOptions {
			if compose.IsSwarmIgnoredOption(option) {
				slog.WarnContext(ctx, "Compose option is ignored by Docker Swarm",
					"stack", stackName, "field", "services."+service.Name+"."+option)
			}
		}
	}
}
