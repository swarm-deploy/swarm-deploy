package secretmanager

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	servicestore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/utils"
)

const cloudSecretsGRPCAddressEnv = "CS_GRPC_ADDR" //nolint:gosec // The value is a network address, not a credential.

type target struct {
	stack          string
	service        string
	kind           string
	address        string
	controllable   bool
	discoveryError error
}

// Resolver discovers Secret Managers from persisted service resources.
type Resolver struct {
	services servicestore.Store
}

// NewResolver creates a Secret Manager resolver backed by the service resource store.
func NewResolver(services servicestore.Store) *Resolver {
	return &Resolver{services: services}
}

func (r *Resolver) resolve(ctx context.Context) ([]target, error) {
	services, err := r.services.ReadAll(ctx)
	if err != nil {
		return nil, err
	}
	resolved := make([]target, 0)
	for _, service := range services {
		if service.Type != stype.SecretManager {
			continue
		}

		kind := utils.ImageName(service.Image)
		candidate := target{
			stack:   service.Stack,
			service: service.Name,
			kind:    kind,
		}
		if kind == cloudSecretsKind {
			rawAddress := strings.TrimSpace(service.Environment[cloudSecretsGRPCAddressEnv])
			if rawAddress != "" {
				candidate.controllable = true
				candidate.address, candidate.discoveryError = controllerAddress(service.Name, rawAddress)
			}
		}

		resolved = append(resolved, candidate)
	}

	return resolved, nil
}

func (r *Resolver) find(ctx context.Context, stack string, service string) (target, bool, error) {
	targets, err := r.resolve(ctx)
	if err != nil {
		return target{}, false, err
	}
	for _, candidate := range targets {
		if candidate.stack == stack && candidate.service == service {
			return candidate, true, nil
		}
	}

	return target{}, false, nil
}

func controllerAddress(serviceName string, rawAddress string) (string, error) {
	trimmed := strings.TrimSpace(rawAddress)
	_, port, err := net.SplitHostPort(trimmed)
	if err != nil && !strings.Contains(trimmed, ":") {
		port = trimmed
		err = nil
	}
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", cloudSecretsGRPCAddressEnv, err)
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("parse %s: invalid port %q", cloudSecretsGRPCAddressEnv, port)
	}

	return net.JoinHostPort(serviceName, port), nil
}
