package compose

import (
	"crypto/sha256"
	"encoding/hex"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"fmt"
	"slices"
)

// Recalculate rebuilds per-service digests from the current effective Compose state.
// The source-level File.Digest is preserved.
func (*fileLoader) Recalculate(file *File) error {
	digests, err := computeDesiredServiceDigests(file)
	if err != nil {
		return err
	}
	file.ServiceDigests = digests
	return nil
}

func computeDesiredServiceDigests(desired *File) (map[string]string, error) {
	digests := make(map[string]string, len(desired.Compose.Services))
	for _, service := range desired.Compose.Services {
		dependencies, fingerprints := desiredServiceDependencies(desired, service)
		digest, err := digestServiceIdentity(dependencies, fingerprints)
		if err != nil {
			return nil, fmt.Errorf("digest service %s: %w", service.Name, err)
		}
		digests[service.Name] = digest
	}
	return digests, nil
}

func desiredServiceDependencies(
	desired *File,
	service Service,
) (Compose, map[string]string) {
	if len(service.Networks.list()) == 0 {
		service.Networks = NewServiceNetworks(&ServiceNetwork{Alias: "default"})
	}
	configs := slices.Clone(service.Configs)
	secrets := slices.Clone(service.Secrets)
	networkAliases := slices.Clone(service.Networks.GetAliases())
	if service.CredentialSpec != nil && service.CredentialSpec.Config != "" {
		configs = append(configs, ObjectRef{Source: service.CredentialSpec.Config})
	}
	for _, job := range service.InitJobs {
		configs = append(configs, job.Configs...)
		secrets = append(secrets, job.Secrets...)
		networkAliases = append(networkAliases, job.Networks.GetAliases()...)
	}

	selectedConfigs := selectDesiredObjects(desired.Compose.Configs, configs)
	selectedSecrets := selectDesiredObjects(desired.Compose.Secrets, secrets)
	fingerprints := make(map[string]string, len(selectedConfigs)+len(selectedSecrets))
	for alias, config := range selectedConfigs {
		copyDesiredResourceFingerprint(fingerprints, desired.ResourceDigests, "configs/"+alias, config.Data)
		definition := *config
		definition.File = ""
		selectedConfigs[alias] = &definition
	}
	for alias, secret := range selectedSecrets {
		copyDesiredResourceFingerprint(fingerprints, desired.ResourceDigests, "secrets/"+alias, secret.Data)
		definition := *secret
		definition.File = ""
		selectedSecrets[alias] = &definition
	}

	return Compose{
		Services: Services{service},
		Networks: selectDesiredNetworks(desired.Compose.Networks, networkAliases),
		Configs:  selectedConfigs,
		Secrets:  selectedSecrets,
		Volumes:  selectDesiredVolumes(desired.Compose.Volumes, service.Volumes),
	}, fingerprints
}

func selectDesiredObjects[T any, Objects ~map[string]*T](objects Objects, refs []ObjectRef) Objects {
	if len(objects) == 0 || len(refs) == 0 {
		return nil
	}
	selected := make(Objects)
	for _, ref := range refs {
		if object := objects[ref.Source]; object != nil {
			selected[ref.Source] = object
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func selectDesiredNetworks(networks map[string]Network, aliases []string) map[string]Network {
	if len(networks) == 0 || len(aliases) == 0 {
		return nil
	}
	selected := make(map[string]Network)
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

func selectDesiredVolumes(volumes Volumes, refs ServiceVolumes) Volumes {
	if len(volumes) == 0 || len(refs.Volumes) == 0 {
		return nil
	}
	selected := make(Volumes)
	for _, ref := range refs.Volumes {
		if ref == nil || ref.EffectiveType() != ServiceVolumeTypeVolume {
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

func copyDesiredResourceFingerprint(target, source map[string]string, name string, data []byte) {
	if data != nil {
		sum := sha256.Sum256(data)
		target[name] = hex.EncodeToString(sum[:])
		return
	}
	if digest, ok := source[name]; ok {
		target[name] = digest
	}
}

func digestServiceIdentity(desired Compose, resourceDigests map[string]string) (string, error) {
	service := &desired.Services[0]
	service.Networks = digestNetworkAttachments(service.Networks)
	service.InitJobs = slices.Clone(service.InitJobs)
	for i := range service.InitJobs {
		service.InitJobs[i].Networks = digestNetworkAttachments(service.InitJobs[i].Networks)
	}
	if err := canonicalizeService(service, true); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		Compose   Compose           `json:"compose"`
		Resources map[string]string `json:"resources"`
	}{Compose: desired, Resources: resourceDigests}, jsonv1.DefaultOptionsV1())
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// digestNetworkAttachments excludes the resolved name. The network definition
// already contributes the source name and properties to the digest.
func digestNetworkAttachments(networks *ServiceNetworks) *ServiceNetworks {
	cloned := cloneNetworkList(networks)
	for i, network := range cloned.list() {
		if network != nil {
			attachment := *network
			attachment.ResolvedName = ""
			cloned.List[i] = &attachment
		}
	}
	return cloned
}
