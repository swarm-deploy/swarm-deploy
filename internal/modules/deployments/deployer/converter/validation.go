package converter

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

const maxFileMode = 0o777

var dockerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func validateStack(stackName string, desired compose.Compose) error {
	if !dockerName.MatchString(stackName) {
		return fmt.Errorf("stackName: expected a valid Docker stack name")
	}

	for _, alias := range sortedKeys(desired.Networks) {
		n := desired.Networks[alias]
		field := "networks." + alias

		creationOptions := n.Driver != "" || len(n.DriverOpts) > 0 ||
			n.Attachable || n.Internal != nil || len(n.Labels.Map) > 0 || n.IPAM != nil
		if n.External && creationOptions {
			return fmt.Errorf("%s: external network cannot declare creation options", field)
		}
	}
	if err := validateObjects(desired); err != nil {
		return err
	}
	names := map[string]bool{}
	for _, service := range desired.Services {
		if !dockerName.MatchString(service.Name) || names[service.Name] {
			return fmt.Errorf("services: names must be valid Docker names and unique")
		}
		names[service.Name] = true
		if err := validateInitNetworks(service, desired.Networks); err != nil {
			return err
		}
		if err := validateService(service); err != nil {
			return fmt.Errorf("services.%s: %w", service.Name, err)
		}
	}
	return nil
}

func validateObjects(desired compose.Compose) error {
	for _, alias := range sortedKeys(desired.Configs) {
		object := desired.Configs[alias]
		if object == nil {
			return fmt.Errorf("configs.%s: null definition", alias)
		}

	}
	for _, alias := range sortedKeys(desired.Secrets) {
		object := desired.Secrets[alias]
		if object == nil {
			return fmt.Errorf("secrets.%s: null definition", alias)
		}

	}
	for _, alias := range sortedKeys(desired.Volumes) {
		object := desired.Volumes[alias]
		if object == nil {
			continue
		} // YAML `volumes: {data: null}` declares an ordinary named volume.

		if object.External && (object.Driver != "" || len(object.DriverOpts) > 0 || len(object.Labels) > 0) {
			return fmt.Errorf("volumes.%s: external volume cannot declare creation options", alias)
		}
	}
	return nil
}

func validateService(service compose.Service) error {
	if err := validateImage(service.Image); err != nil {
		return err
	}

	if len(service.EnvFiles) > 0 {
		return fmt.Errorf("env_file: populate environment files before conversion")
	}

	if err := validateObjectRefs(service); err != nil {
		return err
	}

	if err := validateNetworkAttachments(service.Networks); err != nil {
		return err
	}

	if err := validateMounts(service.Volumes); err != nil {
		return err
	}
	return nil
}

func validateNetworkAttachments(networks *compose.ServiceNetworks) error {
	if networks == nil {
		return nil
	}
	for _, network := range networks.List {
		if network == nil {
			return fmt.Errorf("networks: null attachment")
		}

	}
	return nil
}

func validateObjectRefs(service compose.Service) error {
	if err := validateFileTargets("configs", service.Configs, false); err != nil {
		return err
	}
	if err := validateFileTargets("secrets", service.Secrets, true); err != nil {
		return err
	}
	for _, ref := range append(append([]compose.ObjectRef{}, service.Configs...), service.Secrets...) {

		for _, owner := range []struct{ field, value string }{{"uid", ref.Uid}, {"gid", ref.Gid}} {
			if owner.value != "" {
				if _, err := strconv.ParseUint(owner.value, 10, 32); err != nil {
					return fmt.Errorf("configs/secrets.%s.%s: expected an unsigned numeric ID", ref.Source, owner.field)
				}
			}
		}
		if ref.Mode != nil && *ref.Mode > maxFileMode {
			return fmt.Errorf("configs/secrets.%s.mode: invalid file mode", ref.Source)
		}
	}
	return nil
}

func validateMounts(volumes compose.ServiceVolumes) error {
	for _, volume := range volumes.Volumes {
		if volume == nil {
			return fmt.Errorf("volumes: null mount")
		}
		if len(volume.UnsupportedModes) > 0 {
			return fmt.Errorf("volumes.mode: unsupported short-syntax option")
		}
	}

	return nil
}

func validateInitNetworks(service compose.Service, definitions map[string]compose.Network) error {
	for _, job := range service.InitJobs {
		if job.Networks == nil {
			continue
		}
		for _, network := range job.Networks.List {
			if network == nil {
				return fmt.Errorf("services.%s.x-init-deploy-jobs.networks: null attachment", service.Name)
			}
			_, exists := definitions[network.Alias]
			if !exists && network.Alias != defaultNetwork {
				return fmt.Errorf("services.%s.x-init-deploy-jobs.%s.networks.%s: undefined network",
					service.Name, job.Name, network.Alias)
			}
		}
	}
	return nil
}

func validateFileTargets(field string, refs []compose.ObjectRef, secret bool) error {
	targets := map[string]bool{}
	for _, ref := range refs {
		target, _, _, _ := fileTarget(ref, secret)
		if targets[target] {
			return fmt.Errorf("%s.%s.target: duplicate file target", field, ref.Source)
		}
		targets[target] = true
	}
	return nil
}
