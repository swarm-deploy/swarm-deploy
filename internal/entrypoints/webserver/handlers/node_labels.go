package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (h *handler) AddNodeLabel(
	ctx context.Context,
	req *generated.NodeLabelCreateRequest,
	params generated.AddNodeLabelParams,
) error {
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return withStatusError(http.StatusBadRequest, errors.New("label key is required"))
	}

	return h.setNodeLabel(ctx, swarm.NodeLabelUpdateRequest{
		NodeID: params.ID,
		Key:    key,
		Value:  req.Value,
	})
}

func (h *handler) UpdateNodeLabel(
	ctx context.Context,
	req *generated.NodeLabelUpdateRequest,
	params generated.UpdateNodeLabelParams,
) error {
	key := strings.TrimSpace(params.Key)
	if key == "" {
		return withStatusError(http.StatusBadRequest, errors.New("label key is required"))
	}

	return h.setNodeLabel(ctx, swarm.NodeLabelUpdateRequest{
		NodeID: params.ID,
		Key:    key,
		Value:  req.Value,
	})
}

func (h *handler) DeleteNodeLabel(ctx context.Context, params generated.DeleteNodeLabelParams) error {
	key := strings.TrimSpace(params.Key)
	if key == "" {
		return withStatusError(http.StatusBadRequest, errors.New("label key is required"))
	}

	if err := h.nodeManager.DeleteLabel(ctx, params.ID, key); err != nil {
		return nodeLabelError(err)
	}

	return nil
}

func (h *handler) setNodeLabel(ctx context.Context, req swarm.NodeLabelUpdateRequest) error {
	if strings.TrimSpace(req.NodeID) == "" {
		return withStatusError(http.StatusBadRequest, errors.New("node id is required"))
	}

	if err := h.nodeManager.SetLabel(ctx, req); err != nil {
		return nodeLabelError(err)
	}

	return nil
}

func nodeLabelError(err error) error {
	switch {
	case errors.Is(err, swarm.ErrNodeNotFound):
		return withStatusError(http.StatusNotFound, errors.New("node not found"))
	case errors.Is(err, swarm.ErrNodeUpdateConflict):
		return withStatusError(http.StatusConflict, errors.New("node changed while updating labels; refresh and retry"))
	default:
		return withStatusError(http.StatusInternalServerError, errors.New("unable to update node labels"))
	}
}
