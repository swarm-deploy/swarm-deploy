package handlers

import (
	"testing"

	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
)

func TestToGeneratedSecretsIncludesDescription(t *testing.T) {
	mapped := toGeneratedSecrets([]secretmodel.Secret{{
		ID:          "secret-id",
		Name:        "database-password",
		Description: "Database password",
	}})

	if len(mapped) != 1 {
		t.Fatalf("expected one mapped secret, got %d", len(mapped))
	}

	description := mapped[0].Description
	if !description.Set {
		t.Fatal("expected description to be set")
	}
	if description.Value != "Database password" {
		t.Fatalf("unexpected description: %q", description.Value)
	}
}
