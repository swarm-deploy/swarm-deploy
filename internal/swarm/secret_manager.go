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

func (r *secretManager) List(ctx context.Context, filter ListSecretsFilter) ([]Secret, error) {
	filterArgs := make([]filters.KeyValuePair, 0, len(filter.Names)+stackResourceFilterCount)
	for _, name := range filter.Names {
		filterArgs = append(filterArgs, filters.Arg("name", name))
	}
	if filter.StackName != "" {
		filterArgs = append(
			filterArgs,
			filters.Arg("label", stackNamespaceLabelKey+"="+filter.StackName),
			filters.Arg(
				"label",
				labelsdict.RotatedResourceManagedLabelKey+"="+labelsdict.RotatedResourceManagedLabelValue,
			),
		)
	}

	secrets, err := r.dockerClient.SecretList(ctx, dockerswarm.SecretListOptions{
		Filters: filters.NewArgs(filterArgs...),
	})
	if err != nil {
		return nil, fmt.Errorf("list docker secrets: %w", err)
	}

	mapped := make([]Secret, len(secrets))
	for i, secret := range secrets {
		mapped[i] = r.mapSecretInfo(secret)
	}
	r.sortSecretInfos(mapped)

	return mapped, nil
}

func (r *secretManager) Create(ctx context.Context, req CreateSecretRequest) (string, error) {
	spec := dockerswarm.SecretSpec{
		Annotations: dockerswarm.Annotations{Name: req.Name, Labels: req.Labels},
		Data:        req.Data,
	}
	if req.Driver != "" {
		spec.Driver = &dockerswarm.Driver{Name: req.Driver, Options: req.DriverOptions}
	}
	if req.TemplateDriver != "" {
		spec.Templating = &dockerswarm.Driver{Name: req.TemplateDriver}
	}

	created, err := r.dockerClient.SecretCreate(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("create docker secret %s: %w", req.Name, httpx.MatchError(err))
	}

	return created.ID, nil
}

func (r *secretManager) Remove(ctx context.Context, secretID string) error {
	if err := r.dockerClient.SecretRemove(ctx, secretID); err != nil {
		return fmt.Errorf("remove docker secret %s: %w", secretID, err)
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
		return nil, fmt.Errorf("inspect secret: %w", err)
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
