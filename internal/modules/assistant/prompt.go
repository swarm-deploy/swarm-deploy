package assistant

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/llm"
)

const maxToolRoundsPlaceholder = "{{max_tool_rounds}}"
const replyLanguagePlaceholder = "{{reply_language}}"

const customPromptHeading = "\n\nProject-specific instructions:\n"

//go:embed prompts/base.md
var basePrompt string

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

func buildSystemPrompt(customPrompt, routePrompt, replyLanguage string) string {
	prompt := strings.ReplaceAll(
		strings.TrimSpace(basePrompt), maxToolRoundsPlaceholder, strconv.Itoa(maxToolIterations),
	)
	prompt = strings.ReplaceAll(prompt, replyLanguagePlaceholder, replyLanguage)
	if routePrompt = strings.TrimSpace(routePrompt); routePrompt != "" {
		prompt = fmt.Sprintf("%s\n\n%s", prompt, routePrompt)
	}

	return prompt + customPromptSuffix(customPrompt)
}

func customPromptSuffix(customPrompt string) string {
	customPrompt = strings.TrimSpace(customPrompt)
	if customPrompt == "" {
		return ""
	}
	return customPromptHeading + customPrompt
}

func modelMessageFromTurn(turn conversation.Turn) llm.Message {
	message := llm.Message{Role: turn.Role, Content: turn.Content}
	if turn.Role != "assistant" || len(turn.Artifacts) == 0 {
		return message
	}

	var content strings.Builder
	content.WriteString(turn.Content)
	content.WriteString("\n\nArtifacts:")
	for _, artifact := range turn.Artifacts {
		content.WriteString("\n- ")
		content.WriteString(artifact.Label)
		if artifact.URL != "" {
			content.WriteString(": ")
			content.WriteString(artifact.URL)
		}
	}
	message.Content = content.String()
	return message
}
