package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/livemanifest"
	"gopkg.in/yaml.v3"
)

const yamlOutputIndent = 2

func (h *handler) GetStackManifestos(
	ctx context.Context,
	params generated.GetStackManifestosParams,
) (*generated.StackManifestosResponse, error) {
	composeFile, found := h.resolveStackComposeFile(params.Stack)
	if !found {
		return nil, withStatusError(http.StatusNotFound, fmt.Errorf("stack %s not found", params.Stack))
	}

	desiredManifest, err := h.git.ReadFile(ctx, composeFile)
	if err != nil {
		slog.ErrorContext(
			ctx,
			"[webserver] failed to read desired stack manifest",
			slog.String("stack", params.Stack),
			slog.String("compose_file", composeFile),
			slog.Any("err", err),
		)
		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to get stack desired manifest"))
	}

	services, err := h.serviceInspector.ListStackServices(ctx, params.Stack)
	if err != nil {
		slog.ErrorContext(
			ctx,
			"[webserver] failed to list stack services",
			slog.String("stack", params.Stack),
			slog.String("compose_file", composeFile),
			slog.Any("err", err),
		)
		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to list stack services"))
	}

	liveCompose, err := livemanifest.NewComputer(h.serviceInspector, h.networks).ComputeStack(ctx, livemanifest.Stack{
		Name:     params.Stack,
		Services: services,
	})
	if err != nil {
		slog.ErrorContext(
			ctx,
			"[webserver] failed to compute stack live manifest",
			slog.String("stack", params.Stack),
			slog.Any("err", err),
		)

		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to get stack live manifest"))
	}

	liveManifest := bytes.NewBuffer(nil)

	yamlEncoder := yaml.NewEncoder(liveManifest)
	yamlEncoder.SetIndent(yamlOutputIndent)

	err = yamlEncoder.Encode(liveCompose)
	if err != nil {
		slog.ErrorContext(
			ctx,
			"[webserver] failed to marshal stack live manifest",
			slog.String("stack", params.Stack),
			slog.Any("err", err),
		)

		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to get stack live manifest"))
	}

	return &generated.StackManifestosResponse{
		Desired: string(desiredManifest),
		Live:    liveManifest.String(),
	}, nil
}

func (h *handler) resolveStackComposeFile(stackName string) (string, bool) {
	if h.control == nil {
		return "", false
	}

	stacks := h.control.ListStacks()
	for _, stack := range stacks {
		if stack.Name == stackName {
			return stack.ComposeFile, true
		}
	}

	return "", false
}
