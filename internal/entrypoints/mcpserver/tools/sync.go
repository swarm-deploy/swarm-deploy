package tools

import (
	"context"

	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// Sync triggers manual synchronization run.
type Sync struct {
	control SyncTrigger
}

// NewSync creates sync component.
func NewSync(control SyncTrigger) *Sync {
	return &Sync{control: control}
}

// Definition returns tool metadata visible to the model.
func (s *Sync) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "sync",
		Description: "Triggers manual synchronization run.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

// Execute runs sync tool.
func (s *Sync) Execute(_ context.Context, _ routing.Request) (routing.Response, error) {
	queued := s.control.Trigger(controller.TriggerManual)
	payload := struct {
		Queued bool `json:"queued"`
	}{
		Queued: queued,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
