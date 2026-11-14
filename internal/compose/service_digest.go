package compose

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

const (
	defaultServiceNetwork = "default"
	defaultObjectMode     = 0o444
	secretTargetPrefix    = "/run/secrets/"
)

// DesiredServiceDigests fingerprints each effective service and the resources it uses.
// Call it after preparing the Compose definition. It leaves the source digest and
// the prepared definition intact.
func DesiredServiceDigests(file *File) (map[string]string, error) {
	digests := make(map[string]string, len(file.Compose.Services))
	for _, service := range file.Compose.Services {
		dependencies, fingerprints := desiredServiceDependencies(file, service)
		dependencies.Services = Services{serviceForDigest(service)}
		payload, err := json.Marshal(struct {
			// Compose contains one service and its referenced resource definitions.
			Compose Compose `json:"compose"`
			// Resources contains content hashes for referenced files.
			Resources map[string]string `json:"resources"`
		}{Compose: dependencies, Resources: fingerprints}, json.Deterministic(true), jsontext.ReorderRawObjects(true))
		if err != nil {
			return nil, fmt.Errorf("encode desired state for service %s: %w", service.Name, err)
		}
		digests[service.Name] = digestBytes(payload)
	}
	return digests, nil
}

func serviceForDigest(service Service) Service {
	service.Command = commandForDigest(service.Command)
	service.Entrypoint = commandForDigest(service.Entrypoint)
	if service.Healthcheck != nil {
		health := *service.Healthcheck
		words := health.Test.Args
		if len(words) > 0 && !health.Test.IsList() {
			words = append([]string{"CMD-SHELL"}, words...)
		}
		health.Test = NewCommand(words)
		service.Healthcheck = &health
	}

	service.CapAdd = capabilitiesForDigest(service.CapAdd)
	service.CapDrop = slices.DeleteFunc(capabilitiesForDigest(service.CapDrop), func(value string) bool {
		return slices.Contains(service.CapAdd, value)
	})
	service.EnvFiles = nil
	service.Environment.Keys = nil
	service.Volumes.Map = nil
	service.Ports.Ports = slices.Clone(service.Ports.Ports)
	slices.SortStableFunc(service.Ports.Ports, compareDigestPorts)
	service.Networks = networksForDigest(service.Networks)
	if service.Networks == nil {
		service.Networks = &ServiceNetworks{List: []*ServiceNetwork{{Alias: defaultServiceNetwork}}}
	}
	slices.SortStableFunc(service.Networks.List, func(a, b *ServiceNetwork) int {
		return strings.Compare(a.Alias, b.Alias)
	})

	// Init jobs inherit these references, including their original targets.
	// Apply service defaults only when no init job consumes the references.
	if len(service.InitJobs) == 0 {
		service.Configs = refsForDigest(service.Configs, false)
		service.Secrets = refsForDigest(service.Secrets, true)
	}
	service.InitJobs = slices.Clone(service.InitJobs)
	for i := range service.InitJobs {
		job := &service.InitJobs[i]
		job.Environment.Keys = nil
		job.Networks = networksForDigest(job.Networks)
	}
	return service
}

func commandForDigest(command Command) Command {
	words, err := command.Words()
	if err != nil {
		// The deployment converter owns command validation and its diagnostics.
		return command
	}
	if len(words) == 0 {
		words = nil
	}
	return NewCommand(words)
}

func capabilitiesForDigest(values []string) []string {
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

func compareDigestPorts(a, b ServicePort) int {
	if a.Published != b.Published {
		return cmp.Compare(a.Published, b.Published)
	}
	if a.Target != b.Target {
		return cmp.Compare(a.Target, b.Target)
	}
	if a.Protocol != b.Protocol {
		return cmp.Compare(a.Protocol, b.Protocol)
	}
	if a.Mode != b.Mode {
		return cmp.Compare(a.Mode, b.Mode)
	}
	if a.HostIP != b.HostIP {
		return cmp.Compare(a.HostIP, b.HostIP)
	}
	return cmp.Compare(a.AppProtocol, b.AppProtocol)
}

func networksForDigest(networks *ServiceNetworks) *ServiceNetworks {
	if networks == nil || len(networks.List) == 0 {
		return nil
	}
	list := make([]*ServiceNetwork, len(networks.List))
	for i, attachment := range networks.List {
		network := *attachment
		network.ResolvedName = ""
		list[i] = &network
	}
	return &ServiceNetworks{List: list}
}

func refsForDigest(refs []ObjectRef, secret bool) []ObjectRef {
	refs = slices.Clone(refs)
	for i := range refs {
		ref := &refs[i]
		if ref.Target == "" || ref.ShortForm {
			ref.Target = "/" + ref.Source
			if secret {
				ref.Target = ref.Source
			}
		}
		if secret && !strings.HasPrefix(ref.Target, "/") {
			ref.Target = secretTargetPrefix + ref.Target
		}
		ref.ShortForm = false
		if ref.Uid == "0" {
			ref.Uid = ""
		}
		if ref.Gid == "0" {
			ref.Gid = ""
		}
		if ref.Mode != nil && *ref.Mode == defaultObjectMode {
			ref.Mode = nil
		}
	}
	slices.SortStableFunc(refs, func(a, b ObjectRef) int {
		return strings.Compare(a.Source, b.Source)
	})
	return refs
}

func desiredServiceDependencies(file *File, service Service) (Compose, map[string]string) {
	configs := slices.Clone(service.Configs)
	secrets := slices.Clone(service.Secrets)
	networks := make(map[string]Network)
	selectDigestNetworks(networks, file.Compose.Networks, service.Networks)
	if service.Networks == nil || len(service.Networks.List) == 0 {
		if network, ok := file.Compose.Networks[defaultServiceNetwork]; ok {
			networks[defaultServiceNetwork] = network
		}
	}
	if service.CredentialSpec != nil && service.CredentialSpec.Config != "" {
		configs = append(configs, ObjectRef{Source: service.CredentialSpec.Config})
	}
	for _, job := range service.InitJobs {
		configs = append(configs, job.Configs...)
		secrets = append(secrets, job.Secrets...)
		selectDigestNetworks(networks, file.Compose.Networks, job.Networks)
	}

	fingerprints := make(map[string]string)
	return Compose{
		Networks: networks,
		Configs:   digestConfigs(file, configs, fingerprints),
		Secrets:   digestSecrets(file, secrets, fingerprints),
		Volumes:   digestVolumes(file.Compose.Volumes, service.Volumes.Volumes),
	}, fingerprints
}

func selectDigestNetworks(selected, definitions map[string]Network, attachments *ServiceNetworks) {
	for _, alias := range attachments.GetAliases() {
		if network, ok := definitions[alias]; ok {
			selected[alias] = network
		}
	}
}

func digestConfigs(file *File, refs []ObjectRef, fingerprints map[string]string) Configs {
	selected := make(Configs)
	for _, ref := range refs {
		config := file.Compose.Configs[ref.Source]
		if config == nil {
			continue
		}
		metadata := *config
		metadata.File, metadata.Data = "", nil
		selected[ref.Source] = &metadata
		addDigestFingerprint(fingerprints, file.ResourceDigests, "configs/"+ref.Source, config.Data)
	}
	return selected
}

func digestSecrets(file *File, refs []ObjectRef, fingerprints map[string]string) Secrets {
	selected := make(Secrets)
	for _, ref := range refs {
		secret := file.Compose.Secrets[ref.Source]
		if secret == nil {
			continue
		}
		metadata := *secret
		metadata.File, metadata.Data = "", nil
		selected[ref.Source] = &metadata
		addDigestFingerprint(fingerprints, file.ResourceDigests, "secrets/"+ref.Source, secret.Data)
	}
	return selected
}

func digestVolumes(volumes Volumes, refs []*ServiceVolume) Volumes {
	selected := make(Volumes)
	for _, ref := range refs {
		if ref != nil && ref.EffectiveType() == ServiceVolumeTypeVolume {
			if volume := volumes[ref.Source]; volume != nil {
				selected[ref.Source] = volume
			}
		}
	}
	return selected
}

func addDigestFingerprint(target, source map[string]string, name string, data []byte) {
	if data != nil {
		target[name] = digestBytes(data)
		return
	}
	if digest, ok := source[name]; ok {
		target[name] = digest
	}
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
