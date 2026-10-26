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
	servicemodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	servicestore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
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
	services servicestore.Store
	secrets  secretstore.Store
}

// NewResolver creates a service-secret relation resolver.
func NewResolver(services servicestore.Store, secrets secretstore.Store) *Resolver {
	return &Resolver{services: services, secrets: secrets}
}

// ResolveServiceSecrets resolves persisted metadata for all secret references of a service.
func (r *Resolver) ResolveServiceSecrets(ctx context.Context, info servicemodel.Info) ([]ResolvedSecret, error) {
	resolved := make([]ResolvedSecret, len(info.Spec.Secrets))
	for index, reference := range info.Spec.Secrets {
		resolved[index].Reference = reference
	}

	for index, reference := range info.Spec.Secrets {
		secret, found, err := r.resolveReference(ctx, reference)
		if err != nil {
			return resolved, err
		}

		if found {
			resolved[index].Secret = &secret
		}
	}

	return resolved, nil
}

// ServicesUsingSecret returns services that reference the supplied secret.
func (r *Resolver) ServicesUsingSecret(ctx context.Context, secret secretmodel.Secret) ([]ServiceUsage, error) {
	secrets, err := r.ListSecretsWithUsage(ctx)
	if err != nil {
		return nil, err
	}
	for _, candidate := range secrets {
		if candidate.Secret.ID == secret.ID || candidate.Secret.Name == secret.Name {
			return candidate.UsedBy, nil
		}
	}
	return nil, nil
}

// ListSecretsWithUsage returns all secrets enriched with locally derived service usage.
func (r *Resolver) ListSecretsWithUsage(ctx context.Context) ([]SecretWithUsage, error) {
	secrets, err := r.secrets.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	result := make([]SecretWithUsage, 0, len(secrets))
	for _, secret := range secrets {
		result = append(result, SecretWithUsage{Secret: secret})
	}

	services, err := r.services.ReadAll(ctx)
	if err != nil {
		return result, fmt.Errorf("list services: %w", err)
	}

	secretIndexes := indexSecrets(secrets)

	usages := make([][]ServiceUsage, len(secrets))
	for _, info := range services {
		for _, reference := range info.Spec.Secrets {
			secretIndex, found := referencedSecretIndex(reference, secretIndexes.byID, secretIndexes.byName)
			if !found {
				continue
			}

			usage := ServiceUsage{
				Stack:   info.Stack,
				Service: info.Name,
				Target:  reference.Target,
			}
			for _, relatedIndex := range relatedSecretIndexes(secretIndex, secrets, secretIndexes.byLogicalPath) {
				usages[relatedIndex] = append(usages[relatedIndex], usage)
			}
		}
	}

	for index := range result {
		sortServiceUsages(usages[index])
		result[index].UsedBy = usages[index]
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

type secretIndex struct {
	byID          map[string]int
	byName        map[string]int
	byLogicalPath map[string][]int
}

func indexSecrets(secrets []secretmodel.Secret) secretIndex {
	indexed := secretIndex{
		byID:          make(map[string]int, len(secrets)),
		byName:        make(map[string]int, len(secrets)),
		byLogicalPath: make(map[string][]int),
	}
	for index, secret := range secrets {
		indexed.byID[secret.ID] = index
		indexed.byName[secret.Name] = index
		if logicalPath := strings.TrimSpace(secret.Labels[secretmodel.LogicalPathLabel]); logicalPath != "" {
			indexed.byLogicalPath[logicalPath] = append(indexed.byLogicalPath[logicalPath], index)
		}
	}

	return indexed
}

func relatedSecretIndexes(
	referencedIndex int,
	secrets []secretmodel.Secret,
	byLogicalPath map[string][]int,
) []int {
	logicalPath := strings.TrimSpace(secrets[referencedIndex].Labels[secretmodel.LogicalPathLabel])
	if logicalPath == "" {
		return []int{referencedIndex}
	}

	return byLogicalPath[logicalPath]
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
