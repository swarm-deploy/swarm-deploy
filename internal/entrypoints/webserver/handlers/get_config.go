package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) GetConfig(_ context.Context) (*generated.UIConfigResponse, error) {
	return &generated.UIConfigResponse{
		Web: generated.UIConfigResponseWeb{
			UI: generated.UIConfigResponseWebUI{
				LogViewer: generated.UIConfigResponseWebUILogViewer{
					LinkFields: h.uiConfig.LogViewer.LinkFields,
				},
			},
		},
	}, nil
}
