package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWithStacksFile(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
  - name: worker
    composeFile: worker/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		t.Fatalf("write stacks file: %v", err)
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: pull
stacksFile: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(cfg.Spec.Stacks) != 2 {
		t.Fatalf("expected 2 stacks, got %d", len(cfg.Spec.Stacks))
	}
	if cfg.Spec.Stacks[0].Name != "app" || cfg.Spec.Stacks[1].Name != "worker" {
		t.Fatalf("unexpected stacks order/content: %+v", cfg.Spec.Stacks)
	}
}

func TestLoadFailsWithoutStacksFile(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: pull
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "stacksFile is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWebhookSecretResolveFromFile(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "webhook_secret")
	if err := os.WriteFile(secretFile, []byte(" from-file \n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	spec := WebhookSpec{
		SecretFile: secretFile,
	}

	if got := spec.ResolveSecret(); got != "from-file" {
		t.Fatalf("expected secret from file, got %q", got)
	}
}

func TestLoadResolvesRelativeWebhookSecretFile(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		t.Fatalf("write stacks file: %v", err)
	}

	secretFile := filepath.Join(dir, "webhook_secret")
	if err := os.WriteFile(secretFile, []byte("from-file"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: webhook
  webhook:
    enabled: true
    secretFile: ./webhook_secret
stacksFile: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Spec.Sync.Webhook.SecretFile != secretFile {
		t.Fatalf("expected resolved secretFile %q, got %q", secretFile, cfg.Spec.Sync.Webhook.SecretFile)
	}
	if got := cfg.WebhookSecret(); got != "from-file" {
		t.Fatalf("expected secret from file, got %q", got)
	}
}

func TestLoadIgnoresDataDirFromConfig(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		t.Fatalf("write stacks file: %v", err)
	}

	secretFile := filepath.Join(dir, "webhook_secret")
	if err := os.WriteFile(secretFile, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
dataDir: /tmp/custom-path-should-be-ignored
git:
  repository: https://example.com/repo.git
sync:
  mode: webhook
  webhook:
    enabled: true
    secretFile: ./webhook_secret
stacksFile: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	expectedDataDir := filepath.Join(dir, ".swarm-deploy")
	if cfg.Spec.DataDir != expectedDataDir {
		t.Fatalf("expected dataDir %q, got %q", expectedDataDir, cfg.Spec.DataDir)
	}
}
