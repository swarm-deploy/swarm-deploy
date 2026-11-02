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
	services, err := h.services.ReadAll(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "[webserver] failed to list DSN suggestions", slog.Any("err", err))
		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to list database services"))
	}
	suggestions := make([]generated.DSNSuggestion, 0, len(services))
	for _, service := range services {
		if service.Type != stype.Database {
			continue
		}
		suggestion := generated.DSNSuggestion{
			ServiceName: swarm.NewServiceReference(service.Stack, service.Name).Name(),
			Stack:       service.Stack,
		}
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
	const (
		postgresql  = "postgresql"
		mysqlEngine = "mysql"
	)
	switch utils.ImageName(image) {
	case "postgres", postgresql:
		return postgresql
	case mysqlEngine, "mariadb":
		return mysqlEngine
	}
	var postgres, mysql bool
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, token := range tokens {
		switch token {
		case "postgres", postgresql:
			postgres = true
		case mysqlEngine, "mariadb":
			mysql = true
		}
	}
	if postgres && !mysql {
		return postgresql
	}
	if mysql && !postgres {
		return mysqlEngine
	}
	return ""
}
