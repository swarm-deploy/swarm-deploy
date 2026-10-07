package swarm

import (
	"context"
	"fmt"
	"sort"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

type secretManager struct {
	dockerClient *client.Client
}

func newSecretManager(dockerClient *client.Client) SecretManager {
	return &secretManager{
		dockerClient: dockerClient,
	}
}

func (r *secretManager) List(ctx context.Context) ([]Secret, error) {
	return r.list(ctx, dockerswarm.SecretListOptions{})
}

func (r *secretManager) ListStack(ctx context.Context, stackName string) ([]Secret, error) {
	return r.list(ctx, dockerswarm.SecretListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", stackNamespaceLabelKey+"="+stackName),
			filters.Arg(
				"label",
				labelsdict.RotatedResourceManagedLabelKey+"="+labelsdict.RotatedResourceManagedLabelValue,
			),
		),
	})
}

func (r *secretManager) list(ctx context.Context, options dockerswarm.SecretListOptions) ([]Secret, error) {
	secrets, err := r.dockerClient.SecretList(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("list docker secrets: %w", dockerAPIError(err))
	}

	mapped := make([]Secret, len(secrets))
	for i, secret := range secrets {
		mapped[i] = r.mapSecretInfo(secret)
	}
	r.sortSecretInfos(mapped)

	return mapped, nil
}

func (r *secretManager) Remove(ctx context.Context, secretID string) error {
	if err := r.dockerClient.SecretRemove(ctx, secretID); err != nil {
		return fmt.Errorf("remove docker secret %s: %w", secretID, dockerAPIError(err))
	}

	return nil
}

func (r *secretManager) Watch(ctx context.Context) (<-chan dockerevents.Message, <-chan error, error) {
	eventsFilter := filters.NewArgs(filters.Arg("type", string(dockerevents.SecretEventType)))
	messages, errs := r.dockerClient.Events(ctx, dockerevents.ListOptions{Filters: eventsFilter})

	return messages, errs, nil
}

func (r *secretManager) ResolveReference(
	ctx context.Context,
	source, target string,
) (*dockerswarm.SecretReference, error) {
	secret, _, err := r.dockerClient.SecretInspectWithRaw(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("inspect secret: %w", dockerAPIError(err))
	}

	ref := &dockerswarm.SecretReference{
		SecretID:   secret.ID,
		SecretName: secret.Spec.Name,
	}

	if target == "" {
		target = fmt.Sprintf("/run/secrets/%s", ref.SecretName)
	}

	ref.File = &dockerswarm.SecretReferenceFileTarget{
		Name: target,
		UID:  "0",
		GID:  "0",
		Mode: secretFileMode,
	}

	return ref, nil
}

func (*secretManager) mapSecretInfo(secret dockerswarm.Secret) Secret {
	driver := ""
	if secret.Spec.Driver != nil {
		driver = secret.Spec.Driver.Name
	}

	return Secret{
		ID:        secret.ID,
		VersionID: secret.Version.Index,
		Name:      secret.Spec.Name,
		CreatedAt: secret.CreatedAt,
		UpdatedAt: secret.UpdatedAt,
		Driver:    driver,
		Labels:    secret.Spec.Labels,
	}
}

func (*secretManager) sortSecretInfos(secrets []Secret) {
	sort.Slice(secrets, func(i, j int) bool {
		if secrets[i].Name != secrets[j].Name {
			return secrets[i].Name < secrets[j].Name
		}

		return secrets[i].ID < secrets[j].ID
	})
}
