package assistant

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed prompt.md
var safetyPrompt string

func buildSystemPrompt(customPrompt string, toolNames []string) string {
	prompt := strings.TrimSpace(safetyPrompt)
	if len(toolNames) > 0 {
		prompt = fmt.Sprintf("%s\n\nAvailable tools: %s.", prompt, strings.Join(toolNames, ", "))
	}

	customPrompt = strings.TrimSpace(customPrompt)
	if customPrompt != "" {
		prompt = fmt.Sprintf("%s\n\nProject-specific instructions:\n%s", prompt, customPrompt)
	}

	return prompt
}
