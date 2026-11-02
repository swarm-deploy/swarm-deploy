package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/utils"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ListDSNSuggestions returns database hostnames without reading credentials.
func (h *handler) ListDSNSuggestions(ctx context.Context) (*generated.DSNSuggestionsResponse, error) {
	services, err := h.serviceInspector.ListServices(ctx, swarm.ListServicesFilter{})
	if err != nil {
		slog.ErrorContext(ctx, "[webserver] failed to list DSN suggestions", slog.Any("err", err))
		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to list database services"))
	}
	suggestions := make([]generated.DSNSuggestion, 0, len(services))
	resolver := stype.NewResolver()
	for _, service := range services {
		labels := stype.Labels{Service: service.Labels}
		if container := service.ServiceSpec.TaskTemplate.ContainerSpec; container != nil {
			labels.Container = container.Labels
		}
		if resolver.Resolve(service.Image, labels) != stype.Database {
			continue
		}
		suggestion := generated.DSNSuggestion{ServiceID: service.ID, ServiceName: service.FullName, Stack: service.Stack}
		if engine := inferDSNEngine(service.Image, service.Name); engine != "" {
			suggestion.Engine.SetTo(generated.DSNSuggestionEngine(engine))
		} else {
			suggestion.Engine.SetToNull()
		}
		suggestions = append(suggestions, suggestion)
	}
	return &generated.DSNSuggestionsResponse{Suggestions: suggestions}, nil
}

// Engine inference belongs only to DSN suggestions, not to the global classifier.
func inferDSNEngine(image, name string) string {
	switch utils.ImageName(image) {
	case "postgres", "postgresql":
		return "postgresql"
	case "mysql", "mariadb":
		return "mysql"
	}
	var postgres, mysql bool
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, token := range tokens {
		switch token {
		case "postgres", "postgresql":
			postgres = true
		case "mysql", "mariadb":
			mysql = true
		}
	}
	if postgres && !mysql {
		return "postgresql"
	}
	if mysql && !postgres {
		return "mysql"
	}
	return ""
}
