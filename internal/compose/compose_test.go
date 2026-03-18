package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadInitJobsEnvironment(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yaml")
	composePayload := []byte(`
services:
  api:
    image: ghcr.io/acme/api:1.0.0
    x-init-deploy-jobs:
      - name: migrate
        image: ghcr.io/acme/api:1.0.0
        command: ["./bin/migrate", "up"]
        environment:
          DB_HOST: postgres
          DB_USER: app
`)
	if err := os.WriteFile(composePath, composePayload, 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	file, err := Load(composePath)
	if err != nil {
		t.Fatalf("load compose: %v", err)
	}

	if len(file.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(file.Services))
	}
	if len(file.Services[0].InitJobs) != 1 {
		t.Fatalf("expected 1 init job, got %d", len(file.Services[0].InitJobs))
	}

	first := file.Services[0].InitJobs[0]
	if first.Environment["DB_HOST"] != "postgres" || first.Environment["DB_USER"] != "app" {
		t.Fatalf("unexpected environment for first job: %#v", first.Environment)
	}
}

func TestLoadInitJobsIgnoresLegacyEnv(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yaml")
	composePayload := []byte(`
services:
  api:
    image: ghcr.io/acme/api:1.0.0
    x-init-deploy-jobs:
      - name: legacy
        image: ghcr.io/acme/api:1.0.0
        env:
          LEGACY: "1"
`)
	if err := os.WriteFile(composePath, composePayload, 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	file, err := Load(composePath)
	if err != nil {
		t.Fatalf("load compose: %v", err)
	}

	if len(file.Services) != 1 || len(file.Services[0].InitJobs) != 1 {
		t.Fatalf("unexpected init jobs shape: %#v", file.Services)
	}
	if file.Services[0].InitJobs[0].Environment != nil {
		t.Fatalf("legacy env must be ignored, got: %#v", file.Services[0].InitJobs[0].Environment)
	}
}
