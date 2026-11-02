package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/utils"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	dsnPostgresImage  = "postgres"
	dsnMariaDBImage   = "mariadb"
	dsnPostgreSQL     = "postgresql"
	dsnMySQL          = "mysql"
	dsnPostgreSQLPort = 5432
	dsnMySQLPort      = 3306
	dsnPortBits       = 16
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
		engine := inferDSNEngine(service.Image, service.Name)
		if engine != "" {
			suggestion.Engine.SetTo(generated.DSNSuggestionEngine(engine))
		} else {
			suggestion.Engine.SetToNull()
		}
		if port := suggestDSNPort(service.Image, engine, service.Environment); port > 0 {
			suggestion.Port.SetTo(port)
		} else {
			suggestion.Port.SetToNull()
		}
		suggestions = append(suggestions, suggestion)
	}
	return &generated.DSNSuggestionsResponse{Suggestions: suggestions}, nil
}

// suggestDSNPort reads only server-port variables from the catalog environment.
func suggestDSNPort(image, engine string, environment map[string]string) int {
	var defaultPort int
	var variables []string
	switch engine {
	case dsnPostgreSQL:
		defaultPort = dsnPostgreSQLPort
		variables = []string{"PGPORT"}
	case dsnMySQL:
		defaultPort = dsnMySQLPort
		variables = []string{"MYSQL_TCP_PORT"}
	default:
		return 0
	}
	parts := strings.Split(strings.TrimSpace(image), "/")
	if len(parts) > 1 && (parts[len(parts)-2] == "bitnami" || parts[len(parts)-2] == "bitnamilegacy") {
		switch utils.ImageName(image) {
		case dsnPostgresImage, dsnPostgreSQL:
			variables = []string{"POSTGRESQL_PORT_NUMBER", "POSTGRES_PORT_NUMBER"}
		case dsnMySQL:
			variables = []string{"MYSQL_PORT_NUMBER"}
		case dsnMariaDBImage:
			variables = []string{"MARIADB_PORT_NUMBER"}
		}
	}
	for _, variable := range variables {
		raw := strings.TrimSpace(environment[variable])
		if raw == "" {
			continue
		}
		port, err := strconv.ParseUint(raw, 10, dsnPortBits)
		if err == nil && port > 0 {
			return int(port)
		}
		return defaultPort
	}
	return defaultPort
}

// Engine inference belongs only to DSN suggestions, not to the global classifier.
func inferDSNEngine(image, name string) string {
	switch utils.ImageName(image) {
	case dsnPostgresImage, dsnPostgreSQL:
		return dsnPostgreSQL
	case dsnMySQL, dsnMariaDBImage:
		return dsnMySQL
	}
	var postgres, mysql bool
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, token := range tokens {
		switch token {
		case dsnPostgresImage, dsnPostgreSQL:
			postgres = true
		case dsnMySQL, dsnMariaDBImage:
			mysql = true
		}
	}
	if postgres && !mysql {
		return dsnPostgreSQL
	}
	if mysql && !postgres {
		return dsnMySQL
	}
	return ""
}
