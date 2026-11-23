package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const staleTerminalTaskAge = 12 * time.Hour

// ListSwarmServiceTasks returns current and recent tasks for a Docker service.
func (h *handler) ListSwarmServiceTasks(
	ctx context.Context,
	params generated.ListSwarmServiceTasksParams,
) (*generated.ServiceRealtimeResponse, error) {
	tasks, err := h.serviceInspector.ListTasks(ctx, params.ServiceIDOrName)
	if err == nil {
		nodes, nodeErr := h.nodes.Lookup(ctx)
		if nodeErr != nil {
			return nil, nodeErr
		}
		tasks = filterServiceRealtimeTasks(tasks, time.Now().Add(-staleTerminalTaskAge))
		slices.SortStableFunc(tasks, func(a, b swarm.ServiceTask) int {
			return b.CreatedAt.Compare(a.CreatedAt)
		})

		return &generated.ServiceRealtimeResponse{
			Tasks: toGeneratedServiceRealtimeTasks(tasks, nodes),
		}, nil
	}

	if errors.Is(err, swarm.ErrServiceNotFound) {
		return nil, withStatusError(
			http.StatusNotFound,
			fmt.Errorf("service %s not found", params.ServiceIDOrName),
		)
	}

	slog.ErrorContext(
		ctx,
		"[webserver] failed to read service realtime",
		slog.String("service", params.ServiceIDOrName),
		slog.Any("err", err),
	)
	return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to get service realtime"))
}

func filterServiceRealtimeTasks(tasks []swarm.ServiceTask, cutoff time.Time) []swarm.ServiceTask {
	return slices.DeleteFunc(tasks, func(task swarm.ServiceTask) bool {
		switch task.CurrentState { //nolint:exhaustive // other states not interested
		case swarm.TaskStateShutdown:
			return true
		case swarm.TaskStateFailed, swarm.TaskStateRejected:
			return task.UpdatedAt.Before(cutoff)
		default:
			return false
		}
	})
}
