package assistant

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed prompts/base.md
var basePrompt string

//go:embed prompts/general.md
var generalPrompt string

//go:embed prompts/platform.md
var platformPrompt string

//go:embed prompts/services.md
var servicesPrompt string

//go:embed prompts/cluster.md
var clusterPrompt string

//go:embed prompts/deployments.md
var deploymentsPrompt string

//go:embed prompts/diagnostics.md
var diagnosticsPrompt string

//go:embed prompts/lookups.md
var lookupsPrompt string

func buildSystemPrompt(customPrompt, routePrompt string) string {
	prompt := strings.TrimSpace(basePrompt)
	if routePrompt = strings.TrimSpace(routePrompt); routePrompt != "" {
		prompt = fmt.Sprintf("%s\n\n%s", prompt, routePrompt)
	}

	customPrompt = strings.TrimSpace(customPrompt)
	if customPrompt != "" {
		prompt = fmt.Sprintf("%s\n\nProject-specific instructions:\n%s", prompt, customPrompt)
	}

	return prompt
}
