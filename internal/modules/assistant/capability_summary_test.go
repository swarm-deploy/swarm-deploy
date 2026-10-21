package assistant

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

func TestCapabilitySummaryRespectsRegistrationAndAllowlist(t *testing.T) {
	tests := []struct {
		name string
		allow []string
		want []string
		absent []string
	}{
		{
			name: "all registered",
			want: []string{"service_logs_get", "swarm_node_list", "service_restart_trigger", "deployment_list"},
			absent: []string{"service_task_logs_get", "dns_name_resolve"},
		},
		{
			name: "allowlist",
			allow: []string{"service_logs_get"},
			want: []string{"service_logs_get", "Service catalog metadata"},
			absent: []string{"swarm_node_list", "service_restart_trigger", "deployment_list", "dns_name_resolve"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &graph{
				tools: &fakeTools{definitions: []routing.ToolDefinition{
					{Name: config.AssistantToolName("service_logs_get")},
					{Name: config.AssistantToolName("swarm_node_list")},
					{Name: config.AssistantToolName("service_restart_trigger")},
					{Name: config.AssistantToolName("deployment_list")},
				}},
				allowedToolSet: normalizeAllowedTools(tc.allow),
			}
			summary := g.capabilitySummary()
			for _, want := range tc.want {
				assert.Contains(t, summary, want)
			}
			for _, absent := range tc.absent {
				assert.NotContains(t, summary, absent)
			}
			require.True(t, strings.Contains(summary, "informational"))
			assert.Contains(t, summary, "confirmation required")
		})
	}
}

func TestCapabilitySummaryStable(t *testing.T) {
	g := &graph{
		tools: &fakeTools{definitions: assistantTestToolDefinitions()},
	}
	assert.Equal(t, g.capabilitySummary(), g.capabilitySummary())
	assert.Contains(t, g.capabilitySummary(), "service_logs_get")
	assert.Contains(t, g.capabilitySummary(), "service_task_list")
	assert.NotContains(t, g.capabilitySummary(), "assistant_prompt_injection_report")
}
