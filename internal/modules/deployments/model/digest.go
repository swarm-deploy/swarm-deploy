package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

type desiredDigests struct {
	Stack          string
	StackResources string
	Services       map[string]string
}

func computeDigests(desired compose.Compose, resourceDigests map[string]string) (desiredDigests, error) {
	stackResources, err := digestIdentity(compose.Compose{
		Networks: desired.Networks,
		Configs:  desired.Configs,
		Secrets:  desired.Secrets,
	}, resourceDigests)
	if err != nil {
		return desiredDigests{}, err
	}

	serviceDigests := make(map[string]string, len(desired.Services))
	for _, service := range desired.Services {
		dependencies, fingerprints := serviceDependencies(desired, service, resourceDigests)
		digest, digestErr := digestIdentity(dependencies, fingerprints)
		if digestErr != nil {
			return desiredDigests{}, fmt.Errorf("digest service %s: %w", service.Name, digestErr)
		}
		serviceDigests[service.Name] = digest
	}

	canonical, err := json.Marshal(struct {
		Version        int               `json:"version"`
		StackResources string            `json:"stack_resources"`
		Services       map[string]string `json:"services"`
	}{DigestAlgorithmVersion, stackResources, serviceDigests})
	if err != nil {
		return desiredDigests{}, err
	}
	return desiredDigests{
		Stack: fingerprint(canonical), StackResources: stackResources, Services: serviceDigests,
	}, nil
}

func digestIdentity(desired compose.Compose, resourceDigests map[string]string) (string, error) {
	payload, err := json.Marshal(desired)
	if err != nil {
		return "", err
	}
	var tree map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err = decoder.Decode(&tree); err != nil {
		return "", err
	}
	normalize(tree)
	if services, ok := tree["services"].([]any); ok {
		sortServices(services)
	}
	canonical, err := json.Marshal(struct {
		Version   int               `json:"version"`
		Compose   map[string]any    `json:"compose"`
		Resources map[string]string `json:"resources"`
	}{DigestAlgorithmVersion, tree, resourceDigests})
	if err != nil {
		return "", err
	}
	return fingerprint(canonical), nil
}

func sortServices(services []any) {
	slices.SortStableFunc(services, func(left, right any) int {
		leftService, _ := left.(map[string]any)
		rightService, _ := right.(map[string]any)
		return compare(fmt.Sprint(leftService["name"]), fmt.Sprint(rightService["name"]))
	})
}

func compare(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func serviceDependencies(
	desired compose.Compose,
	service compose.Service,
	resourceDigests map[string]string,
) (compose.Compose, map[string]string) {
	configs := slices.Clone(service.Configs)
	secrets := slices.Clone(service.Secrets)
	networkAliases := service.Networks.GetAliases()
	if len(networkAliases) == 0 {
		networkAliases = []string{"default"}
	}
	if service.CredentialSpec != nil && service.CredentialSpec.Config != "" {
		configs = append(configs, compose.ObjectRef{Source: service.CredentialSpec.Config})
	}
	for _, job := range service.InitJobs {
		configs = append(configs, job.Configs...)
		secrets = append(secrets, job.Secrets...)
		networkAliases = append(networkAliases, job.Networks.GetAliases()...)
	}

	selectedConfigs := selectObjects(desired.Configs, configs)
	selectedSecrets := selectObjects(desired.Secrets, secrets)
	dependencies := compose.Compose{
		Services: compose.Services{service},
		Networks: selectNetworks(desired.Networks, networkAliases),
		Configs:  selectedConfigs,
		Secrets:  selectedSecrets,
		Volumes:  selectVolumes(desired.Volumes, service.Volumes),
	}
	fingerprints := make(map[string]string)
	for name := range selectedConfigs {
		copyFingerprint(fingerprints, resourceDigests, "configs/"+name)
	}
	for name := range selectedSecrets {
		copyFingerprint(fingerprints, resourceDigests, "secrets/"+name)
	}
	return dependencies, fingerprints
}

func selectObjects[T any, Objects ~map[string]*T](objects Objects, refs []compose.ObjectRef) Objects {
	if len(objects) == 0 || len(refs) == 0 {
		return nil
	}
	selected := make(Objects)
	for _, ref := range refs {
		if object, ok := objects[ref.Source]; ok {
			selected[ref.Source] = object
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func selectNetworks(networks map[string]compose.Network, aliases []string) map[string]compose.Network {
	if len(networks) == 0 || len(aliases) == 0 {
		return nil
	}
	selected := make(map[string]compose.Network)
	for _, alias := range aliases {
		if network, ok := networks[alias]; ok {
			selected[alias] = network
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func selectVolumes(volumes compose.Volumes, refs compose.ServiceVolumes) compose.Volumes {
	if len(volumes) == 0 || len(refs.Volumes) == 0 {
		return nil
	}
	selected := make(compose.Volumes)
	for _, ref := range refs.Volumes {
		if ref.EffectiveType() != compose.ServiceVolumeTypeVolume {
			continue
		}
		if volume, ok := volumes[ref.Source]; ok {
			selected[ref.Source] = volume
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func copyFingerprint(target, source map[string]string, name string) {
	if digest, ok := source[name]; ok {
		target[name] = digest
	}
}
