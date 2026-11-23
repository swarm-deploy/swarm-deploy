package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/artarts36/envmasker"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// GetSwarmTask returns inspected Docker task details with recoverable sensitive environment values.
func (h *handler) GetSwarmTask(
	ctx context.Context,
	params generated.GetSwarmTaskParams,
) (*generated.SwarmTaskDetailsHeaders, error) {
	task, err := h.serviceInspector.GetTask(ctx, params.TaskID)
	if errors.Is(err, swarm.ErrTaskNotFound) {
		return nil, withStatusError(http.StatusNotFound, fmt.Errorf("task %s not found", params.TaskID))
	}
	if err != nil {
		slog.ErrorContext(
			ctx,
			"[webserver] failed to inspect swarm task",
			slog.String("task_id", params.TaskID),
			slog.Any("err", err),
		)

		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to get swarm task"))
	}

	return &generated.SwarmTaskDetailsHeaders{
		CacheControl: "no-store",
		Response:     toGeneratedSwarmTaskDetails(task),
	}, nil
}

func toGeneratedSwarmTaskDetails(task swarm.ServiceTaskDetails) generated.SwarmTaskDetails {
	mapped := generated.SwarmTaskDetails{
		ID:           task.ID,
		ServiceID:    task.ServiceID,
		NodeID:       task.NodeID,
		Slot:         int64(task.Slot),
		DesiredState: string(task.DesiredState),
		CurrentState: string(task.CurrentState),
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
		ContainerID:  task.ContainerID,
		Image:        task.Image,
		Environment:  toGeneratedEnvironment(task.Environment),
	}
	if task.Error != "" {
		mapped.Error = generated.NewOptString(task.Error)
	}
	if task.ExitCode != nil {
		mapped.ExitCode = generated.NewOptInt64(int64(*task.ExitCode))
	}
	if task.Reservations != nil {
		mapped.Reservations = generated.NewOptSwarmTaskResources(toGeneratedTaskResources(*task.Reservations))
	}
	if task.Limits != nil {
		mapped.Limits = generated.NewOptSwarmTaskResources(toGeneratedTaskResources(*task.Limits))
	}

	return mapped
}

func toGeneratedEnvironment(entries []string) []generated.EnvironmentVariable {
	mapped := make([]generated.EnvironmentVariable, 0, len(entries))
	for _, entry := range entries {
		name, value, _ := strings.Cut(entry, "=")
		mapped = append(mapped, toGeneratedEnvironmentVariable(name, value))
	}

	return mapped
}

func toGeneratedEnvironmentVariable(name string, value string) generated.EnvironmentVariable {
	_, sensitive := envmasker.Mask(name, value)
	if sensitive {
		return generated.EnvironmentVariable{
			Name:    name,
			Value:   base64.StdEncoding.EncodeToString([]byte(value)),
			Encoded: true,
		}
	}

	return generated.EnvironmentVariable{
		Name:    name,
		Value:   value,
		Encoded: false,
	}
}

func toGeneratedTaskResources(resources swarm.TaskResources) generated.SwarmTaskResources {
	return generated.SwarmTaskResources{
		NanoCpus:    resources.NanoCPUs,
		MemoryBytes: resources.MemoryBytes,
	}
}
