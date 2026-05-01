package tools

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// Sync triggers manual synchronization run.
type Sync struct {
	control SyncTrigger
}

// NewSync creates deploy_sync_trigger component.
func NewSync(control SyncTrigger) *Sync {
	return &Sync{control: control}
}

// Definition returns tool metadata visible to the model.
func (s *Sync) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "deploy_sync_trigger",
		Description: "Triggers manual synchronization run.",
		ParametersJSONSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Request: struct{}{},
	}
}

// Execute runs deploy_sync_trigger tool.
func (s *Sync) Execute(ctx context.Context, _ routing.Request) (routing.Response, error) {
	queued := s.control.Manual(ctx)
	payload := struct {
		Queued bool `json:"queued"`
	}{
		Queued: queued,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}
