package compose

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

// DesiredServiceDigests fingerprints the services and their referenced resources
// after effective Compose preparation. It leaves the source definition untouched.
func (f *File) DesiredServiceDigests() (map[string]string, error) {
	digests := make(map[string]string, len(f.Compose.Services))
	for _, service := range f.Compose.Services {
		identity := f.serviceIdentity(normalizeDigestService(service))
		data, err := json.Marshal(identity, json.Deterministic(true))
		if err != nil {
			return nil, fmt.Errorf("digest service %q: %w", service.Name, err)
		}
		digest := sha256.Sum256(data)
		digests[service.Name] = hex.EncodeToString(digest[:])
	}
	return digests, nil
}

type serviceIdentity struct {
	// Service holds the effective service and its Init Jobs.
	Service Service
	// Networks holds the referenced network definitions.
	Networks map[string]Network
	// Configs holds the referenced config definitions.
	Configs Configs
	// Secrets holds the referenced secret definitions.
	Secrets Secrets
	// Volumes holds the referenced named volume definitions.
	Volumes Volumes
	// ResourceDigests fingerprints the referenced config and secret file contents.
	ResourceDigests map[string]string
}

func (f *File) serviceIdentity(service Service) serviceIdentity {
	identity := serviceIdentity{
		Service: service, Networks: map[string]Network{},
		Configs: Configs{}, Secrets: Secrets{}, Volumes: Volumes{},
		ResourceDigests: map[string]string{},
	}
	addDigestNetworks(identity.Networks, f.Compose.Networks, service.Networks)
	for _, ref := range service.Configs {
		f.addDigestConfig(&identity, ref.Source)
	}
	for _, ref := range service.Secrets {
		f.addDigestSecret(&identity, ref.Source)
	}
	if service.CredentialSpec != nil {
		f.addDigestConfig(&identity, service.CredentialSpec.Config)
	}
	for _, job := range service.InitJobs {
		addDigestNetworks(identity.Networks, f.Compose.Networks, job.Networks)
		for _, ref := range job.Configs {
			f.addDigestConfig(&identity, ref.Source)
		}
		for _, ref := range job.Secrets {
			f.addDigestSecret(&identity, ref.Source)
		}
	}
	for _, mount := range service.Volumes.Volumes {
		if mount != nil && mount.EffectiveType() == ServiceVolumeTypeVolume {
			if volume, ok := f.Compose.Volumes[mount.Source]; ok {
				identity.Volumes[mount.Source] = volume
			}
		}
	}
	return identity
}

func addDigestNetworks(selected, networks map[string]Network, attachments *ServiceNetworks) {
	if attachments == nil {
		return
	}
	for _, attachment := range attachments.List {
		if attachment == nil {
			continue
		}
		if network, ok := networks[attachment.Alias]; ok {
			selected[attachment.Alias] = network
		}
	}
}

func (f *File) addDigestConfig(identity *serviceIdentity, alias string) {
	if config := f.Compose.Configs[alias]; config != nil && identity.Configs[alias] == nil {
		copyConfig := *config
		copyConfig.File = ""
		identity.Configs[alias] = &copyConfig
		f.addDigestResource(identity.ResourceDigests, "configs/"+alias, config.Data)
	}
}

func (f *File) addDigestSecret(identity *serviceIdentity, alias string) {
	if secret := f.Compose.Secrets[alias]; secret != nil && identity.Secrets[alias] == nil {
		copySecret := *secret
		copySecret.File = ""
		identity.Secrets[alias] = &copySecret
		f.addDigestResource(identity.ResourceDigests, "secrets/"+alias, secret.Data)
	}
}

func (f *File) addDigestResource(digests map[string]string, key string, data []byte) {
	if data != nil {
		digest := sha256.Sum256(data)
		digests[key] = hex.EncodeToString(digest[:])
	} else if digest, ok := f.ResourceDigests[key]; ok {
		digests[key] = digest
	}
}

func normalizeDigestService(service Service) Service {
	service.Command = digestCommand(service.Command)
	service.Entrypoint = digestCommand(service.Entrypoint)
	if service.Healthcheck != nil {
		health := *service.Healthcheck
		if !health.Test.IsList() && len(health.Test.Args) != 0 {
			health.Test = NewCommand(append([]string{"CMD-SHELL"}, health.Test.Args...))
		}
		if len(health.Test.Args) == 0 {
			health.Test = NewCommand(nil)
		}
		service.Healthcheck = &health
	}

	service.CapAdd = normalizeDigestCapabilities(service.CapAdd)
	service.CapDrop = normalizeDigestCapabilities(service.CapDrop)
	service.CapDrop = slices.DeleteFunc(service.CapDrop, func(capability string) bool {
		return slices.Contains(service.CapAdd, capability)
	})
	service.EnvFiles = nil // Preparation already merged their values into Environment.
	service.Environment.Keys = nil
	service.Volumes.Map = nil
	service.Ports.Ports = slices.Clone(service.Ports.Ports)
	slices.SortStableFunc(service.Ports.Ports, func(a, b ServicePort) int {
		return cmp.Compare(a.Published, b.Published)
	})
	service.Networks = digestNetworks(service.Networks)
	if service.Networks == nil {
		service.Networks = &ServiceNetworks{List: []*ServiceNetwork{{Alias: "default"}}}
	}
	service.Configs = digestObjectRefs(service.Configs, false, false)
	service.Secrets = digestObjectRefs(service.Secrets, true, false)
	service.InitJobs = slices.Clone(service.InitJobs)
	for i := range service.InitJobs {
		job := &service.InitJobs[i]
		job.Environment.Keys = nil
		job.Networks = digestNetworks(job.Networks)
		job.Configs = digestObjectRefs(job.Configs, false, true)
		job.Secrets = digestObjectRefs(job.Secrets, true, true)
	}
	return service
}

func digestCommand(command Command) Command {
	words, err := command.Words()
	if err != nil {
		// Keep validation and error reporting in the existing execution path.
		return command
	}
	if len(words) == 0 {
		words = nil
	}
	return NewCommand(words)
}

func normalizeDigestCapabilities(values []string) []string {
	var result []string
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "RESET" {
			continue
		}
		if value == "ALL" {
			return []string{"ALL"}
		}
		if !strings.HasPrefix(value, "CAP_") {
			value = "CAP_" + value
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func digestNetworks(networks *ServiceNetworks) *ServiceNetworks {
	if networks == nil || len(networks.List) == 0 {
		return nil
	}
	normalized := &ServiceNetworks{List: make([]*ServiceNetwork, 0, len(networks.List))}
	for _, attachment := range networks.List {
		if attachment == nil {
			normalized.List = append(normalized.List, nil)
			continue
		}
		network := *attachment
		network.ResolvedName = "" // The referenced network definition contains the name.
		normalized.List = append(normalized.List, &network)
	}
	slices.SortStableFunc(normalized.List, func(a, b *ServiceNetwork) int {
		return cmp.Compare(networkAttachmentAlias(a), networkAttachmentAlias(b))
	})
	return normalized
}

func networkAttachmentAlias(attachment *ServiceNetwork) string {
	if attachment == nil {
		return ""
	}
	return attachment.Alias
}

func digestObjectRefs(refs []ObjectRef, secret, initJob bool) []ObjectRef {
	normalized := slices.Clone(refs)
	for i := range normalized {
		ref := &normalized[i]
		ref.Target = digestObjectTarget(*ref, secret, initJob)
		ref.ShortForm = false
		if initJob && !secret && ref.Target == "" {
			ref.Uid, ref.Gid, ref.Mode = "", "", nil
			continue
		}
		if ref.Uid == "0" {
			ref.Uid = ""
		}
		if ref.Gid == "0" {
			ref.Gid = ""
		}
		const defaultMode = 0o444
		if ref.Mode != nil && *ref.Mode == defaultMode {
			ref.Mode = nil
		}
	}
	if !initJob {
		slices.SortStableFunc(normalized, func(a, b ObjectRef) int {
			return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Target, b.Target))
		})
	}
	return normalized
}

func digestObjectTarget(ref ObjectRef, secret, initJob bool) string {
	target := ref.Target
	if initJob {
		if secret && target == "" {
			return "/run/secrets/" + ref.Source
		}
		return target
	}
	if !secret {
		if target == "" || ref.ShortForm {
			return "/" + ref.Source
		}
		return target
	}
	if target == "" || ref.ShortForm {
		target = ref.Source
	}
	if !strings.HasPrefix(target, "/") {
		return "/run/secrets/" + target
	}
	return target
}
