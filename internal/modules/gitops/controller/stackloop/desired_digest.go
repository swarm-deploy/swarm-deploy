package stackloop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func computeDesiredServiceDigests(desired *compose.File) (map[string]string, error) {
	digests := make(map[string]string, len(desired.Compose.Services))
	for _, source := range desired.Compose.Services {
		service, err := canonicalDigestService(source)
		if err != nil {
			return nil, fmt.Errorf("normalize service %s: %w", source.Name, err)
		}
		dependencies, fingerprints := desiredServiceDependencies(desired, service)
		digest, err := digestServiceIdentity(dependencies, fingerprints)
		if err != nil {
			return nil, fmt.Errorf("digest service %s: %w", source.Name, err)
		}
		digests[source.Name] = digest
	}
	return digests, nil
}

func canonicalDigestService(service compose.Service) (compose.Service, error) {
	for _, command := range []struct {
		name  string
		value *compose.Command
	}{
		{name: "command", value: &service.Command},
		{name: "entrypoint", value: &service.Entrypoint},
	} {
		words, err := command.value.Words()
		if err != nil {
			return service, fmt.Errorf("%s: invalid quoting", command.name)
		}
		if len(words) == 0 {
			words = nil
		}
		*command.value = compose.NewCommand(words)
	}
	if service.Healthcheck != nil {
		health := *service.Healthcheck
		words := slices.Clone(health.Test.Args)
		if len(words) > 0 && !health.Test.IsList() {
			words = append([]string{"CMD-SHELL"}, words...)
		}
		if len(words) == 0 {
			words = nil
		}
		health.Test = compose.NewCommand(words)
		service.Healthcheck = &health
	}
	service.CapAdd = normalizeDigestCapabilities(service.CapAdd)
	service.CapDrop = normalizeDigestCapabilities(service.CapDrop)
	service.CapDrop = slices.DeleteFunc(service.CapDrop, func(value string) bool {
		return slices.Contains(service.CapAdd, value)
	})
	if service.Networks == nil || len(service.Networks.List) == 0 {
		service.Networks = compose.NewServiceNetworks(&compose.ServiceNetwork{Alias: "default"})
	}
	return service, nil
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

func desiredServiceDependencies(
	desired *compose.File,
	service compose.Service,
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

	selectedConfigs := selectDesiredObjects(desired.Compose.Configs, configs)
	selectedSecrets := selectDesiredObjects(desired.Compose.Secrets, secrets)
	fingerprints := make(map[string]string, len(selectedConfigs)+len(selectedSecrets))
	for alias, config := range selectedConfigs {
		copyDesiredResourceFingerprint(fingerprints, desired.ResourceDigests, "configs/"+alias, config.Data)
	}
	for alias, secret := range selectedSecrets {
		copyDesiredResourceFingerprint(fingerprints, desired.ResourceDigests, "secrets/"+alias, secret.Data)
	}

	return compose.Compose{
		Services: compose.Services{service},
		Networks: selectDesiredNetworks(desired.Compose.Networks, networkAliases),
		Configs:  selectedConfigs,
		Secrets:  selectedSecrets,
		Volumes:  selectDesiredVolumes(desired.Compose.Volumes, service.Volumes),
	}, fingerprints
}

func selectDesiredObjects[T any, Objects ~map[string]*T](objects Objects, refs []compose.ObjectRef) Objects {
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

func selectDesiredNetworks(networks map[string]compose.Network, aliases []string) map[string]compose.Network {
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

func selectDesiredVolumes(volumes compose.Volumes, refs compose.ServiceVolumes) compose.Volumes {
	if len(volumes) == 0 || len(refs.Volumes) == 0 {
		return nil
	}
	selected := make(compose.Volumes)
	for _, ref := range refs.Volumes {
		if ref == nil || ref.EffectiveType() != compose.ServiceVolumeTypeVolume {
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

func digestServiceIdentity(desired compose.Compose, resourceDigests map[string]string) (string, error) {
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
	removeDesiredResourcePaths(tree, "configs")
	removeDesiredResourcePaths(tree, "secrets")
	normalizeDesiredDigestTree(tree)
	canonical, err := json.Marshal(struct {
		Compose   map[string]any    `json:"compose"`
		Resources map[string]string `json:"resources"`
	}{Compose: tree, Resources: resourceDigests})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func removeDesiredResourcePaths(tree map[string]any, field string) {
	objects, _ := tree[field].(map[string]any)
	for _, value := range objects {
		object, _ := value.(map[string]any)
		delete(object, "file")
	}
}

func normalizeDesiredDigestTree(value any) {
	switch node := value.(type) {
	case map[string]any:
		normalizeDesiredObjectRefs(node, "secrets")
		normalizeDesiredObjectRefs(node, "configs")
		delete(node, "Keys")
		delete(node, "env_file")
		if _, isVolumeList := node["Volumes"]; isVolumeList {
			delete(node, "Map")
		}
		if _, isNetworkList := node["List"]; isNetworkList {
			delete(node, "AliasMap")
			delete(node, "Names")
			delete(node, "Aliases")
		}
		delete(node, "ResolvedName")
		for _, child := range node {
			normalizeDesiredDigestTree(child)
		}
		for _, key := range []string{"Ports", "Volumes", "List", "cap_add", "cap_drop", "secrets", "configs"} {
			if list, ok := node[key].([]any); ok {
				sort.SliceStable(list, func(i, j int) bool {
					left, _ := json.Marshal(list[i])
					right, _ := json.Marshal(list[j])
					return bytes.Compare(left, right) < 0
				})
			}
		}
	case []any:
		for _, child := range node {
			normalizeDesiredDigestTree(child)
		}
	}
}

func normalizeDesiredObjectRefs(node map[string]any, key string) {
	refs, _ := node[key].([]any)
	for _, value := range refs {
		ref, ok := value.(map[string]any)
		if !ok {
			continue
		}
		normalizeDesiredObjectRef(ref, key)
	}
}

func normalizeDesiredObjectRef(ref map[string]any, key string) {
	source, _ := ref["source"].(string)
	target, _ := ref["target"].(string)
	short, _ := ref["short_form"].(bool)
	ref["target"] = desiredObjectTarget(key, source, target, short)
	delete(ref, "short_form")
	for _, owner := range []string{"uid", "gid"} {
		if ref[owner] == nil || ref[owner] == "" || ref[owner] == "0" {
			delete(ref, owner)
		}
	}
	if ref["mode"] == nil || ref["mode"] == json.Number("292") { // Docker's default file mode, 0444.
		delete(ref, "mode")
	}
}

func desiredObjectTarget(key, source, target string, short bool) string {
	if key == "configs" && (short || target == "") {
		return "/" + source
	}
	if key != "secrets" {
		return target
	}
	if target == "" || short {
		target = source
	}
	if !strings.HasPrefix(target, "/") {
		target = "/run/secrets/" + target
	}
	return target
}
