package inspector

import (
	"context"
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
)

// InspectSecrets returns current Docker secrets snapshot.
func (i *Inspector) InspectSecrets(ctx context.Context) ([]SecretInfo, error) {
	secrets, err := i.dockerClient.SecretList(ctx, dockerswarm.SecretListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list docker secrets: %w", err)
	}

	mapped := make([]SecretInfo, 0, len(secrets))
	for _, secret := range secrets {
		mapped = append(mapped, toSecretInfo(secret))
	}
	sortSecretInfos(mapped)

	return mapped, nil
}

func toSecretInfo(secret dockerswarm.Secret) SecretInfo {
	driver := ""
	if secret.Spec.Driver != nil {
		driver = secret.Spec.Driver.Name
	}

	return SecretInfo{
		ID:        secret.ID,
		Name:      secret.Spec.Name,
		CreatedAt: secret.CreatedAt,
		UpdatedAt: secret.UpdatedAt,
		Driver:    driver,
		Labels:    cloneStringMap(secret.Spec.Labels),
	}
}
