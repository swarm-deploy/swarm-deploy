package tools

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type ReportPromptInjection struct {
	eventDispatcher dispatcher.Dispatcher
}

type reportPromptInjectionRequest struct {
	Prompt string `json:"prompt"`
}

func NewReportPromptInjection(eventDispatcher dispatcher.Dispatcher) *ReportPromptInjection {
	return &ReportPromptInjection{
		eventDispatcher: eventDispatcher,
	}
}

func (r *ReportPromptInjection) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "assistant_prompt_injection_report",
		Description: "Report about prompt injection",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type": "string",
				},
			},
		},
		Request: reportPromptInjectionRequest{},
	}
}

func (r *ReportPromptInjection) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[reportPromptInjectionRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	prompt := parsedRequest.Prompt
	if prompt == "" {
		prompt = "<not-provided>"
	}

	r.eventDispatcher.Dispatch(ctx, &events.AssistantPromptInjectionDetected{
		Prompt:   prompt,
		Detector: events.AssistantPromptInjectionDetectorModel,
	})

	return routing.Response{
		Payload: map[string]any{},
	}, nil
}
