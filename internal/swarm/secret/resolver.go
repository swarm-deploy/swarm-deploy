package secret

import (
	"context"
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

const secretOrConfigFileMode = 0o444

type Resolver struct {
	dockerClient *client.Client
}

func NewResolver(dockerClient *client.Client) *Resolver {
	return &Resolver{
		dockerClient: dockerClient,
	}
}

func (r *Resolver) ResolveReference(ctx context.Context, source, target string) (*dockerswarm.SecretReference, error) {
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
		Mode: secretOrConfigFileMode,
	}

	return ref, nil
}
