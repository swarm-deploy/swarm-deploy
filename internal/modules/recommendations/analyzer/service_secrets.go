package analyzer

import (
	"context"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

type secretEnvironmentVariable struct {
	name     string
	fileName string
}

// These file variables are supported by the official images' entrypoint scripts.
// Keep the list explicit: file-based credentials are an image-specific convention.
var serviceSecretVariables = map[string][]secretEnvironmentVariable{
	"docker.io/library/postgres": {
		{"POSTGRES_PASSWORD", "POSTGRES_PASSWORD_FILE"},
	},
	"docker.io/library/mysql": {
		{"MYSQL_ROOT_PASSWORD", "MYSQL_ROOT_PASSWORD_FILE"},
		{"MYSQL_PASSWORD", "MYSQL_PASSWORD_FILE"},
	},
	"docker.io/library/mariadb": {
		{"MARIADB_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD_FILE"},
		{"MARIADB_PASSWORD", "MARIADB_PASSWORD_FILE"},
	},
	"docker.io/library/mongo": {
		{"MONGO_INITDB_ROOT_PASSWORD", "MONGO_INITDB_ROOT_PASSWORD_FILE"},
	},
	"docker.io/grafana/grafana": {
		{"GF_SECURITY_ADMIN_PASSWORD", "GF_SECURITY_ADMIN_PASSWORD__FILE"},
		{"GF_DATABASE_PASSWORD", "GF_DATABASE_PASSWORD__FILE"},
		{"GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET", "GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET__FILE"},
		{"GF_AUTH_GOOGLE_CLIENT_SECRET", "GF_AUTH_GOOGLE_CLIENT_SECRET__FILE"},
	},
}

// ServiceSecretsAnalyzer recommends file-based credentials for supported images.
type ServiceSecretsAnalyzer struct {
	now func() time.Time
}

// NewServiceSecretsAnalyzer creates a service credential recommendation analyzer.
func NewServiceSecretsAnalyzer() *ServiceSecretsAnalyzer {
	return &ServiceSecretsAnalyzer{now: time.Now}
}

// Analyze inspects environment names without reading or reporting credential values.
func (a *ServiceSecretsAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)
	for _, service := range stack.Definition.Compose.Services {
		replacements := serviceSecretReplacements(service)
		if len(replacements) == 0 {
			continue
		}

		recs = append(recs, model.Recommendation{
			Type:     model.TypeServiceSecretsEnvironment,
			Severity: model.SeverityMedium,
			Source:   model.SourceFromStack(stack),
			Subject:  model.Subject{Stack: stack.Name, Service: service.Name},
			Title:    "Use Docker secrets for sensitive environment variables",
			Recommendation: "Sensitive environment variables detected. " +
				"Use the file-based alternatives supported by this image: " +
				strings.Join(replacements, "; ") + ". Mount Docker Swarm Secrets under /run/secrets/ and set each " +
				"file-based variable to its secret file path. Remove the plaintext credential variables from the " +
				"service environment (including env_file) after migration.",
			CreatedAt: a.now(),
		})
	}
	return recs
}

func serviceSecretReplacements(service compose.Service) []string {
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(service.Image))
	if err != nil {
		return nil
	}

	image := named.Name()
	if reference.Domain(named) == "registry-1.docker.io" {
		image = "docker.io/" + reference.Path(named)
	}
	var replacements []string
	for _, variable := range serviceSecretVariables[image] {
		if serviceHasEnvironmentVariable(service, variable.name) {
			replacements = append(replacements, variable.name+" -> "+variable.fileName)
		}
	}
	return replacements
}

func serviceHasEnvironmentVariable(service compose.Service, name string) bool {
	if service.Environment.Has(name) {
		return true
	}
	// FileLoader has already loaded these variables. Prepared definitions instead
	// contain the merged environment produced by compose.EnvFilePopulator.
	for _, file := range service.EnvFiles {
		if _, ok := file.Variables[name]; ok {
			return true
		}
	}
	return false
}
