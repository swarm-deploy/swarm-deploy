package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

func TestToolDefinitionsDeclareWriteableAndTerminalBehavior(t *testing.T) {
	testCases := []struct {
		name      string
		tool      routing.Tool
		writeable bool
		terminal  bool
	}{
		{name: "deploy sync", tool: NewSync(nil), writeable: true, terminal: true},
		{name: "service replicas", tool: NewSetServiceReplicas(nil, nil), writeable: true, terminal: true},
		{name: "service restart", tool: NewRestartService(nil, nil), writeable: true, terminal: true},
		{name: "pause set", tool: NewSyncPauseSet(nil), writeable: true, terminal: true},
		{name: "pause resume", tool: NewSyncPauseResume(nil), writeable: true, terminal: true},
		{name: "pause get", tool: NewSyncPauseGet(nil)},
		{name: "prompt injection report", tool: NewReportPromptInjection(nil), terminal: true},
		{name: "service spec", tool: NewGetServiceSpec(nil)},
		{name: "date", tool: NewDate()},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			definition := testCase.tool.Definition()
			assert.Equal(t, testCase.writeable, definition.Writeable, "writeable flag")
			assert.Equal(t, testCase.terminal, definition.Terminal, "terminal flag")
		})
	}
}
