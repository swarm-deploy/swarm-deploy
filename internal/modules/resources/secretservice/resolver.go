// Package secretservice resolves relations between persisted service and secret snapshots.
package secretservice

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ResolvedSecret keeps service-specific usage data together with optional persisted metadata.
type ResolvedSecret struct {
	// Reference is the secret reference stored in the service snapshot.
	Reference swarm.ServiceSecret
	// Secret is persisted metadata, or nil when the reference is stale.
	Secret *secretmodel.Secret
}

// ServiceUsage identifies a service that consumes a secret.
type ServiceUsage struct {
	// Stack is the stack containing the service.
	Stack string
	// Service is the service name inside the stack.
	Service string
	// Target is the target file path inside the service container.
	Target string
}

// SecretWithUsage combines secret metadata with services derived from the service snapshot.
type SecretWithUsage struct {
	// Secret is persisted secret metadata.
	Secret secretmodel.Secret
	// UsedBy contains services consuming the secret.
	UsedBy []ServiceUsage
}

// Resolver joins persisted service and secret snapshots without querying Docker.
type Resolver struct {
	services *service.Store
	secrets  secretstore.Store
}

// NewResolver creates a service-secret relation resolver.
func NewResolver(services *service.Store, secrets secretstore.Store) *Resolver {
	return &Resolver{services: services, secrets: secrets}
}

// ResolveServiceSecrets resolves persisted metadata for all secret references of a service.
func (r *Resolver) ResolveServiceSecrets(ctx context.Context, info service.Info) ([]ResolvedSecret, error) {
	resolved := make([]ResolvedSecret, 0, len(info.Spec.Secrets))
	for _, reference := range info.Spec.Secrets {
		secret, found, err := r.resolveReference(ctx, reference)
		if err != nil {
			return nil, err
		}

		item := ResolvedSecret{Reference: reference}
		if found {
			item.Secret = &secret
		}
		resolved = append(resolved, item)
	}

	return resolved, nil
}

// ServicesUsingSecret returns services that reference the supplied secret.
func (r *Resolver) ServicesUsingSecret(secret secretmodel.Secret) []ServiceUsage {
	usedBy := make([]ServiceUsage, 0)
	for _, info := range r.services.List() {
		for _, reference := range info.Spec.Secrets {
			if !referencesSecret(reference, secret) {
				continue
			}

			usedBy = append(usedBy, ServiceUsage{
				Stack:   info.Stack,
				Service: info.Name,
				Target:  reference.Target,
			})
		}
	}

	sortServiceUsages(usedBy)

	return usedBy
}

// ListSecretsWithUsage returns all secrets enriched with locally derived service usage.
func (r *Resolver) ListSecretsWithUsage(ctx context.Context) ([]SecretWithUsage, error) {
	secrets, err := r.secrets.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}

	byID := make(map[string]int, len(secrets))
	byName := make(map[string]int, len(secrets))
	for index, secret := range secrets {
		byID[secret.ID] = index
		byName[secret.Name] = index
	}

	usages := make([][]ServiceUsage, len(secrets))
	for _, info := range r.services.List() {
		for _, reference := range info.Spec.Secrets {
			secretIndex, found := referencedSecretIndex(reference, byID, byName)
			if !found {
				continue
			}

			usages[secretIndex] = append(usages[secretIndex], ServiceUsage{
				Stack:   info.Stack,
				Service: info.Name,
				Target:  reference.Target,
			})
		}
	}

	result := make([]SecretWithUsage, 0, len(secrets))
	for index, secret := range secrets {
		sortServiceUsages(usages[index])
		result = append(result, SecretWithUsage{
			Secret: secret,
			UsedBy: usages[index],
		})
	}

	return result, nil
}

func (r *Resolver) resolveReference(
	ctx context.Context,
	reference swarm.ServiceSecret,
) (secretmodel.Secret, bool, error) {
	if id := strings.TrimSpace(reference.SecretID); id != "" {
		secret, err := r.secrets.GetByID(ctx, id)
		if err == nil {
			return secret, true, nil
		}
		if !errors.Is(err, secretstore.ErrSecretNotFound) {
			return secretmodel.Secret{}, false, fmt.Errorf("get secret by id: %w", err)
		}
	}

	if name := strings.TrimSpace(reference.SecretName); name != "" {
		secret, err := r.secrets.GetByName(ctx, name)
		if err == nil {
			return secret, true, nil
		}
		if !errors.Is(err, secretstore.ErrSecretNotFound) {
			return secretmodel.Secret{}, false, fmt.Errorf("get secret by name: %w", err)
		}
	}

	return secretmodel.Secret{}, false, nil
}

func referencesSecret(reference swarm.ServiceSecret, secret secretmodel.Secret) bool {
	referenceID := strings.TrimSpace(reference.SecretID)
	secretID := strings.TrimSpace(secret.ID)
	if referenceID != "" && secretID != "" && referenceID == secretID {
		return true
	}

	return strings.TrimSpace(reference.SecretName) != "" && reference.SecretName == secret.Name
}

func referencedSecretIndex(
	reference swarm.ServiceSecret,
	byID map[string]int,
	byName map[string]int,
) (int, bool) {
	if id := strings.TrimSpace(reference.SecretID); id != "" {
		if index, found := byID[id]; found {
			return index, true
		}
	}

	name := strings.TrimSpace(reference.SecretName)
	if name == "" {
		return 0, false
	}

	index, found := byName[name]
	return index, found
}

func sortServiceUsages(usages []ServiceUsage) {
	sort.Slice(usages, func(i, j int) bool {
		if usages[i].Stack != usages[j].Stack {
			return usages[i].Stack < usages[j].Stack
		}
		if usages[i].Service != usages[j].Service {
			return usages[i].Service < usages[j].Service
		}
		return usages[i].Target < usages[j].Target
	})
}
