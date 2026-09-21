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

func (h *handler) GetServiceRealtime(
	ctx context.Context,
	params generated.GetServiceRealtimeParams,
) (*generated.ServiceRealtimeResponse, error) {
	tasks, err := h.serviceInspector.ListTasks(ctx, swarm.NewServiceReference(params.Stack, params.Service))
	if err == nil {
		tasks = filterStaleTerminalTasks(tasks, time.Now().Add(-staleTerminalTaskAge))
		slices.SortStableFunc(tasks, func(a, b swarm.ServiceTask) int {
			return b.CreatedAt.Compare(a.CreatedAt)
		})

		return &generated.ServiceRealtimeResponse{
			Tasks: toGeneratedServiceRealtimeTasks(tasks, h.nodes.Map()),
		}, nil
	}

	if errors.Is(err, swarm.ErrServiceNotFound) {
		return nil, withStatusError(
			http.StatusNotFound,
			fmt.Errorf("service %s/%s not found", params.Stack, params.Service),
		)
	}

	slog.ErrorContext(
		ctx,
		"[webserver] failed to read service realtime",
		slog.String("stack", params.Stack),
		slog.String("service", params.Service),
		slog.Any("err", err),
	)
	return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to get service realtime"))
}

func filterStaleTerminalTasks(tasks []swarm.ServiceTask, cutoff time.Time) []swarm.ServiceTask {
	return slices.DeleteFunc(tasks, func(task swarm.ServiceTask) bool {
		switch task.CurrentState { //nolint:exhaustive // other states not interested
		case swarm.TaskStateShutdown, swarm.TaskStateFailed, swarm.TaskStateRejected:
			return task.UpdatedAt.Before(cutoff)
		default:
			return false
		}
	})
}
