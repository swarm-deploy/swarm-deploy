package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

func TestToolDefinitionsDeclareMutationAndTerminalBehavior(t *testing.T) {
	testCases := []struct {
		name     string
		tool     routing.Tool
		mutating bool
		terminal bool
	}{
		{name: "deploy sync", tool: NewSync(nil), mutating: true, terminal: true},
		{name: "service replicas", tool: NewSetServiceReplicas(nil, nil), mutating: true, terminal: true},
		{name: "service restart", tool: NewRestartService(nil, nil), mutating: true, terminal: true},
		{name: "pause set", tool: NewSyncPauseSet(nil), mutating: true, terminal: true},
		{name: "pause resume", tool: NewSyncPauseResume(nil), mutating: true, terminal: true},
		{name: "pause get", tool: NewSyncPauseGet(nil)},
		{name: "prompt injection report", tool: NewReportPromptInjection(nil), terminal: true},
		{name: "service spec", tool: NewGetServiceSpec(nil)},
		{name: "date", tool: NewDate()},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			definition := testCase.tool.Definition()
			assert.Equal(t, testCase.mutating, definition.Mutating, "mutating flag")
			assert.Equal(t, testCase.terminal, definition.Terminal, "terminal flag")
		})
	}
}
