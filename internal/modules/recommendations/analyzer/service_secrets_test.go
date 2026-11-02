package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

const credentialSentinel = "credential-must-never-appear-in-output"

func TestServiceSecretsAnalyzerVariables(t *testing.T) {
	tests := []struct{ image, name, replacement string }{
		{"postgres:18", "POSTGRES_PASSWORD", "POSTGRES_PASSWORD_FILE"},
		{"mysql:8", "MYSQL_ROOT_PASSWORD", "MYSQL_ROOT_PASSWORD_FILE"},
		{"mysql:8", "MYSQL_PASSWORD", "MYSQL_PASSWORD_FILE"},
		{"mariadb:11", "MARIADB_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD_FILE"},
		{"mariadb:11", "MARIADB_PASSWORD", "MARIADB_PASSWORD_FILE"},
		{"mongo:8", "MONGO_INITDB_ROOT_PASSWORD", "MONGO_INITDB_ROOT_PASSWORD_FILE"},
		{"grafana/grafana:12", "GF_SECURITY_ADMIN_PASSWORD", "GF_SECURITY_ADMIN_PASSWORD__FILE"},
		{"grafana/grafana:12", "GF_DATABASE_PASSWORD", "GF_DATABASE_PASSWORD__FILE"},
		{"grafana/grafana:12", "GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET", "GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET__FILE"},
		{"grafana/grafana:12", "GF_AUTH_GOOGLE_CLIENT_SECRET", "GF_AUTH_GOOGLE_CLIENT_SECRET__FILE"},
	}
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, syntax := range []string{"map", "list"} {
				t.Run(syntax, func(t *testing.T) {
					environment := "      " + tt.name + ": " + credentialSentinel
					if syntax == "list" {
						environment = "      - " + tt.name + "=" + credentialSentinel
					}
					definition, err := compose.Parse([]byte("services:\n  app:\n    image: " + tt.image +
						"\n    environment:\n" + environment + "\n"))
					require.NoError(t, err)
					stack := recommendationTestStack(definition.Services...)
					analyzer := NewServiceSecretsAnalyzer()
					analyzer.now = func() time.Time { return now }
					recs := analyzer.Analyze(t.Context(), stack)
					requireNoCredentialOutput(t, recs)
					require.Len(t, recs, 1)
					rec := recs[0]
					assert.Equal(t, model.Type("service.secrets.environment"), rec.Type)
					assert.Equal(t, model.SeverityMedium, rec.Severity)
					assert.Equal(t, model.Subject{Stack: "payments", Service: "app"}, rec.Subject)
					assert.Equal(t, model.SourceFromStack(stack), rec.Source)
					assert.Equal(t, now, rec.CreatedAt)
					assert.Equal(t, "Use Docker secrets for sensitive environment variables", rec.Title)
					assert.Contains(t, rec.Recommendation, tt.name+" -> "+tt.replacement+".")
					assert.Contains(t, rec.Recommendation, "Docker Swarm Secrets under /run/secrets/")
					assert.Contains(t, rec.Recommendation, "Remove the plaintext credential variables")
					assert.Equal(t, "payments-app-compose.yamlservice.secrets.environment", rec.ID())
					assert.True(t, stack.Definition.Compose.Services[0].Environment.Has(tt.name), "analysis must not alter environment")
					env := stack.Definition.Compose.Services[0].Environment.Map
					env[tt.replacement] = "/run/secrets/password"
					recs = analyzer.Analyze(t.Context(), stack)
					requireNoCredentialOutput(t, recs)
					assert.Len(t, recs, 1, "both forms must retain the recommendation")
					delete(env, tt.name)
					recs = analyzer.Analyze(t.Context(), stack)
					requireNoCredentialOutput(t, recs)
					assert.Empty(t, recs, "file-based variables alone must not trigger recommendations")
				})
			}
		})
	}
}

func TestServiceSecretsAnalyzerImageReferences(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("a", 64)
	tests := []struct{ image, name string }{
		{"postgres", "POSTGRES_PASSWORD"},
		{"mysql:8", "MYSQL_PASSWORD"},
		{"mariadb:11", "MARIADB_PASSWORD"},
		{"mongo:8", "MONGO_INITDB_ROOT_PASSWORD"},
		{"grafana/grafana:12", "GF_DATABASE_PASSWORD"},
	}
	for _, tt := range tests {
		for _, prefix := range []string{"", "docker.io/", "index.docker.io/", "registry-1.docker.io/"} {
			for _, suffix := range []string{"", digest} {
				image := tt.image
				if prefix != "" && !strings.Contains(image, "/") {
					image = "library/" + image
				}
				image = prefix + image + suffix
				t.Run(image, func(t *testing.T) {
					stack := recommendationTestStack(compose.Service{Name: "app", Image: image,
						Environment: compose.Environment{Map: map[string]string{tt.name: credentialSentinel}}})
					recs := NewServiceSecretsAnalyzer().Analyze(t.Context(), stack)
					requireNoCredentialOutput(t, recs)
					assert.Len(t, recs, 1)
				})
			}
		}
	}
}

func TestServiceSecretsAnalyzerUnsupportedImages(t *testing.T) {
	for _, image := range []string{
		"", "not a reference", "postgres:bad:tag", "postgres@sha256:invalid", "nginx:latest",
		"bitnami/postgresql:18", "thirdparty/postgres:18", "postgres-custom:18", "postgresql:18",
		"thirdparty/mysql:8", "mysql/mysql-server:8", "thirdparty/mariadb:11", "mariadb/server:11",
		"thirdparty/mongo:8", "mongodb/mongodb-community-server:8", "grafana/grafana-enterprise:12",
		"thirdparty/grafana:12", "registry.example/library/postgres:18", "ghcr.io/grafana/grafana:12",
		"docker.io/thirdparty/postgres:18", "docker.io.evil/library/postgres:18",
	} {
		t.Run(image, func(t *testing.T) {
			environment := map[string]string{}
			for _, variables := range serviceSecretVariables {
				for _, variable := range variables {
					environment[variable.name] = credentialSentinel
				}
			}
			stack := recommendationTestStack(compose.Service{Name: "app", Image: image,
				Environment: compose.Environment{Map: environment}})
			recs := NewServiceSecretsAnalyzer().Analyze(t.Context(), stack)
			requireNoCredentialOutput(t, recs)
			assert.Empty(t, recs)
		})
	}
}

func TestServiceSecretsAnalyzerEnvironmentPresence(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		count int
	}{
		{name: "absent environment"},
		{name: "unrelated variables", env: map[string]string{"POSTGRES_USER": "app", "MYSQL_PASSWORD": credentialSentinel}},
		{name: "case sensitive names", env: map[string]string{"postgres_password": credentialSentinel}},
		{name: "only file alternative", env: map[string]string{"POSTGRES_PASSWORD_FILE": "/run/secrets/password"}},
		{name: "empty value still present", env: map[string]string{"POSTGRES_PASSWORD": ""}, count: 1},
		{name: "both forms", env: map[string]string{
			"POSTGRES_PASSWORD": credentialSentinel, "POSTGRES_PASSWORD_FILE": "/run/secrets/password",
		}, count: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stack := recommendationTestStack(compose.Service{Name: "db", Image: "postgres:18",
				Environment: compose.Environment{Map: tt.env}})
			recs := NewServiceSecretsAnalyzer().Analyze(t.Context(), stack)
			requireNoCredentialOutput(t, recs)
			assert.Len(t, recs, tt.count)
		})
	}
}

func TestServiceSecretsAnalyzerLoadedEnvFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	raw := "services:\n  db:\n    image: mysql:8\n    env_file: [first.env, second.env]\n" +
		"    environment:\n      MYSQL_PASSWORD: " + credentialSentinel + "\n"
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	for _, name := range []string{"first.env", "second.env"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(
			"MYSQL_ROOT_PASSWORD="+credentialSentinel+"\nMYSQL_PASSWORD="+credentialSentinel+"\n"), 0o600))
	}
	file, err := compose.NewFileLoader().Load(t.Context(), path)
	require.NoError(t, err)
	analyzer := NewServiceSecretsAnalyzer()
	stack := recommendationTestStack()
	stack.Definition = *file
	for _, stage := range []string{"loaded", "populated"} {
		t.Run(stage, func(t *testing.T) {
			if stage == "populated" {
				require.True(t, compose.NewEnvFilePopulator().Populate(&stack.Definition))
			}
			recs := analyzer.Analyze(t.Context(), stack)
			requireNoCredentialOutput(t, recs)
			require.Len(t, recs, 1)
			for _, variable := range []string{"MYSQL_ROOT_PASSWORD", "MYSQL_PASSWORD"} {
				assert.Contains(t, recs[0].Recommendation, variable+" -> "+variable+"_FILE")
				assert.Equal(t, 1, strings.Count(recs[0].Recommendation, variable+" -> "))
			}
			assert.Equal(t, model.SourceFromStack(stack), recs[0].Source)
		})
	}
}

func TestServiceSecretsAnalyzerChanges(t *testing.T) {
	analyzer := NewServiceSecretsAnalyzer()
	stack := recommendationTestStack(
		compose.Service{Name: "db", Image: "mysql:8", Environment: compose.Environment{Map: map[string]string{
			"MYSQL_PASSWORD": credentialSentinel, "MYSQL_ROOT_PASSWORD": credentialSentinel,
		}}},
		compose.Service{Name: "other", Image: "postgres:18", Environment: compose.Environment{Map: map[string]string{
			"POSTGRES_PASSWORD": credentialSentinel,
		}}},
	)
	recs := analyzer.Analyze(t.Context(), stack)
	requireNoCredentialOutput(t, recs)
	require.Len(t, recs, 2)
	id := recs[0].ID()
	delete(stack.Definition.Compose.Services[0].Environment.Map, "MYSQL_PASSWORD")
	stack.Definition.Digest = "changed"
	recs = analyzer.Analyze(t.Context(), stack)
	requireNoCredentialOutput(t, recs)
	require.Len(t, recs, 2)
	assert.Equal(t, id, recs[0].ID())
	assert.NotContains(t, recs[0].Recommendation, "MYSQL_PASSWORD -> ")
	delete(stack.Definition.Compose.Services[0].Environment.Map, "MYSQL_ROOT_PASSWORD")
	recs = analyzer.Analyze(t.Context(), stack)
	requireNoCredentialOutput(t, recs)
	require.Len(t, recs, 1)
	assert.Equal(t, "other", recs[0].Subject.Service)
}

func requireNoCredentialOutput(t *testing.T, recs []model.Recommendation) {
	t.Helper()
	encoded, err := json.Marshal(recs)
	require.NoError(t, err)
	// Boolean assertions keep credential values out of test failure output too.
	require.False(t, strings.Contains(string(encoded), credentialSentinel), "credential appeared in recommendation output")
}
