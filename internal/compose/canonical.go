package compose

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	canonicalConfigsField = "configs"
	canonicalSecretsField = "secrets"
)

// Canonicalize returns deterministic JSON for an effective Compose definition.
// It normalizes equivalent execution syntax without mutating desired.
func Canonicalize(desired Compose) ([]byte, error) {
	normalized, err := canonicalizeExecutionSyntax(desired)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode effective Compose: %w", err)
	}
	var tree map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err = decoder.Decode(&tree); err != nil {
		return nil, fmt.Errorf("decode effective Compose: %w", err)
	}
	normalizeEffectiveTree(tree)
	canonical, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("encode canonical Compose: %w", err)
	}
	return canonical, nil
}

func canonicalizeExecutionSyntax(desired Compose) (Compose, error) {
	desired.Services = slices.Clone(desired.Services)
	for i := range desired.Services {
		service := &desired.Services[i]
		for _, field := range []struct {
			name    string
			command *Command
		}{{"command", &service.Command}, {"entrypoint", &service.Entrypoint}} {
			words, err := field.command.Words()
			if err != nil {
				return desired, fmt.Errorf("services.%s.%s: invalid quoting", service.Name, field.name)
			}
			if len(words) == 0 {
				words = nil
			}
			*field.command = NewCommand(words)
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
			health.Test = NewCommand(words)
			service.Healthcheck = &health
		}
		service.CapAdd, service.CapDrop = NormalizeCapabilities(service.CapAdd, service.CapDrop)
	}
	return desired, nil
}

func normalizeEffectiveTree(tree map[string]any) {
	services, _ := tree["services"].([]any)
	for _, value := range services {
		service, ok := value.(map[string]any)
		if !ok {
			continue
		}
		normalizeServiceTree(service)
	}
	sort.SliceStable(services, func(i, j int) bool {
		left, _ := services[i].(map[string]any)
		right, _ := services[j].(map[string]any)
		return fmt.Sprint(left["name"]) < fmt.Sprint(right["name"])
	})
}

func normalizeServiceTree(service map[string]any) {
	delete(service, "env_file")
	normalizeEnvironmentTree(service["environment"])
	normalizeVolumeTree(service["volumes"])
	normalizeNetworkTree(service["networks"])
	normalizeSortedWrapperList(service["ports"], "Ports")
	normalizeObjectRefs(service, canonicalSecretsField, false)
	normalizeObjectRefs(service, canonicalConfigsField, false)
	sortJSONList(service, "cap_add")
	sortJSONList(service, "cap_drop")

	initJobs, _ := service["init_jobs"].([]any)
	for _, value := range initJobs {
		job, ok := value.(map[string]any)
		if !ok {
			continue
		}
		normalizeEnvironmentTree(job["environment"])
		normalizeNetworkTree(job["networks"])
		normalizeObjectRefs(job, canonicalSecretsField, true)
		normalizeObjectRefs(job, canonicalConfigsField, true)
	}
}

func normalizeEnvironmentTree(value any) {
	environment, _ := value.(map[string]any)
	delete(environment, "Keys")
}

func normalizeVolumeTree(value any) {
	volumes, _ := value.(map[string]any)
	delete(volumes, "Map")
	// Mount order controls the order passed to Docker and must remain intact.
}

func normalizeNetworkTree(value any) {
	networks, _ := value.(map[string]any)
	delete(networks, "AliasMap")
	delete(networks, "Names")
	delete(networks, "Aliases")
	sortJSONList(networks, "List")
}

func normalizeSortedWrapperList(value any, key string) {
	wrapper, _ := value.(map[string]any)
	sortJSONList(wrapper, key)
}

func normalizeObjectRefs(node map[string]any, key string, initJob bool) {
	refs, _ := node[key].([]any)
	for _, value := range refs {
		ref, ok := value.(map[string]any)
		if !ok {
			continue
		}
		normalizeObjectRef(ref, key, initJob)
	}
	if !initJob {
		sortJSONList(node, key)
	}
}

func normalizeObjectRef(ref map[string]any, key string, initJob bool) {
	if initJob {
		normalizeInitJobObjectRef(ref, key)
		return
	}
	normalizeServiceObjectRef(ref, key)
}

func normalizeServiceObjectRef(ref map[string]any, key string) {
	source, _ := ref["source"].(string)
	target, _ := ref["target"].(string)
	short, _ := ref["short_form"].(bool)
	if key == canonicalConfigsField && (short || target == "") {
		target = "/" + source
	} else if key == canonicalSecretsField {
		if target == "" || short {
			target = source
		}
		if !strings.HasPrefix(target, "/") {
			target = "/run/secrets/" + target
		}
	}
	ref["target"] = target
	delete(ref, "short_form")
	normalizeObjectOwnership(ref)
}

func normalizeInitJobObjectRef(ref map[string]any, key string) {
	target, _ := ref["target"].(string)
	if key == canonicalSecretsField && target == "" {
		target = "/run/secrets/" + fmt.Sprint(ref["source"])
	}
	ref["target"] = target
	delete(ref, "short_form")
	if key == canonicalConfigsField && target == "" {
		delete(ref, "target")
		delete(ref, "uid")
		delete(ref, "gid")
		delete(ref, "mode")
		return
	}
	normalizeObjectOwnership(ref)
}

func normalizeObjectOwnership(ref map[string]any) {
	for _, owner := range []string{"uid", "gid"} {
		if ref[owner] == nil || ref[owner] == "" || ref[owner] == "0" {
			delete(ref, owner)
		}
	}
	if ref["mode"] == nil || ref["mode"] == json.Number("292") { // Docker's default file mode, 0444.
		delete(ref, "mode")
	}
}

func sortJSONList(node map[string]any, key string) {
	list, ok := node[key].([]any)
	if !ok {
		return
	}
	sort.SliceStable(list, func(i, j int) bool {
		left, _ := json.Marshal(list[i])
		right, _ := json.Marshal(list[j])
		return bytes.Compare(left, right) < 0
	})
}
