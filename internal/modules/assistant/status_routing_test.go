package assistant

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAssistantOverallStatusRoutingGuidance(t *testing.T) {
	assert.Contains(t, routerSystemPrompt, `"Как у нас дела?" -> {"route":"diagnostics"`)
	assert.Contains(t, routerSystemPrompt, `"Как ты?" -> {"route":"general"`)
	assert.Contains(t, routerSystemPrompt, "overall current environment health/status")
	assert.Contains(t, routerSystemPrompt, "Interpret vague health/status questions")

	assert.Contains(t, generalPrompt, "Do not use this route for questions that can reasonably refer to the current swarm-deploy environment")
	assert.Contains(t, diagnosticsPrompt, "broad questions about the current environment health")
	assert.Contains(t, diagnosticsPrompt, "first inspect actual current state")
}
