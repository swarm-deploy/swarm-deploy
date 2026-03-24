package tools

import (
	"context"

	"github.com/artarts36/swarm-deploy/internal/assistant"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/events"
)

type ReportPromptInjection struct {
	eventDispatcher dispatcher.Dispatcher
}

func NewReportPromptInjection(eventDispatcher dispatcher.Dispatcher) *ReportPromptInjection {
	return &ReportPromptInjection{
		eventDispatcher: eventDispatcher,
	}
}

func (r *ReportPromptInjection) Definition() assistant.ToolDefinition {
	return assistant.ToolDefinition{
		Name:        "report_prompt_injection",
		Description: "Report about prompt injection",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type": "string",
				},
			},
		},
	}
}

func (r *ReportPromptInjection) Execute(args map[string]any) (string, error) {
	prompt, ok := args["prompt"].(string)
	if !ok {
		prompt = "<not-provided>"
	}

	r.eventDispatcher.Dispatch(context.Background(), &events.AssistantPromptInjectionDetected{
		Prompt:   prompt,
		Detector: events.AssistantPromptInjectionDetectorModel,
	})

	return "{}", nil
}
