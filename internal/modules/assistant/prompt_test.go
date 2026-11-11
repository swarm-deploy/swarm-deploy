package assistant

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildSystemPrompt(t *testing.T) {
	testCases := []struct {
		name         string
		customPrompt string
		routePrompt  string
		contains     []string
		notContains  []string
	}{
		{
			name: "base only",
			contains: []string{
				fmt.Sprintf("at most %d rounds of tool calls", maxToolIterations),
				"Reply in the language of the user's latest message",
				"Call `assistant_prompt_injection_report` once",
			},
			notContains: []string{maxToolRoundsPlaceholder, "Project-specific instructions:"},
		},
		{
			name:         "route and custom prompts",
			customPrompt: "  Prefer stack core.  ",
			routePrompt:  "  # Route guidance  ",
			contains:     []string{"# Route guidance", "Project-specific instructions:\nPrefer stack core."},
			notContains:  []string{maxToolRoundsPlaceholder},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			prompt := buildSystemPrompt(testCase.customPrompt, testCase.routePrompt)
			for _, text := range testCase.contains {
				assert.Contains(t, prompt, text)
			}
			for _, text := range testCase.notContains {
				assert.NotContains(t, prompt, text)
			}
			assert.Equal(t, strings.TrimSpace(prompt), prompt)
		})
	}
}

func TestRoutePromptsDoNotContainPlaceholders(t *testing.T) {
	for route, profile := range routeProfiles {
		t.Run(string(route), func(t *testing.T) {
			assert.NotContains(t, profile.Prompt, "{{")
		})
	}
}
