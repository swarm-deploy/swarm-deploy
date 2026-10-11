package tools

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
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
		Name:        config.AssistantToolNameAssistantPromptInjectionReport,
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

	err = r.eventDispatcher.Publish(ctx, &events.AssistantPromptInjectionDetected{
		Prompt:   prompt,
		Detector: events.AssistantPromptInjectionDetectorModel,
	})

	if err != nil {
		return routing.Response{}, err
	}
	return routing.Response{
		Payload: map[string]any{},
	}, nil
}
