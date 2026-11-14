package compose

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/utils"
)

const (
	// ConfigsField is the Compose and snapshot key of config references and definitions.
	ConfigsField = "configs"
	// SecretsField is the Compose and snapshot key of secret references and definitions.
	SecretsField = "secrets"

	// dockerSecretsDir is where Docker mounts secrets whose target is relative or empty.
	dockerSecretsDir = "/run/secrets/"
)

// Canonicalize returns deterministic JSON for an effective Compose definition.
// It normalizes equivalent command, entrypoint and healthcheck syntax, capabilities,
// insignificant list ordering and Docker defaults of object references without
// mutating desired.
//
// The output feeds effective desired-state digests stored in existing deployment
// baselines. Any change to it changes those digests and starts one Deployment per
// affected stack on the next sync, which also runs every Init Job of the stack.
// Keep the output stable and update the persisted-digest tests deliberately.
func Canonicalize(desired Compose) ([]byte, error) {
	normalized, err := canonicalizeServiceSyntax(desired)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(normalized, jsonv1.DefaultOptionsV1())
	if err != nil {
		return nil, fmt.Errorf("encode effective Compose: %w", err)
	}
	var tree map[string]any
	if err = json.Unmarshal(payload, &tree, utils.PreserveJSONNumbers()); err != nil {
		return nil, fmt.Errorf("decode effective Compose: %w", err)
	}
	normalizeEffectiveTree(tree)
	canonical, err := json.Marshal(tree, jsonv1.DefaultOptionsV1())
	if err != nil {
		return nil, fmt.Errorf("encode canonical Compose: %w", err)
	}
	return canonical, nil
}

// canonicalizeServiceSyntax normalizes typed service fields whose equivalent
// spellings differ in JSON: execution syntax and capabilities. Capabilities are
// already canonical after decoding; services built in code are used as given.
func canonicalizeServiceSyntax(desired Compose) (Compose, error) {
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
	normalizeObjectRefs(service, SecretsField, false)
	normalizeObjectRefs(service, ConfigsField, false)
	// NormalizeCapabilities sorts plain strings. Re-sort as JSON like earlier releases:
	// names with spaces or escaped characters order differently, and their digests must hold.
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
		normalizeObjectRefs(job, SecretsField, true)
		normalizeObjectRefs(job, ConfigsField, true)
	}
}

func normalizeEnvironmentTree(value any) {
	environment, _ := value.(map[string]any)
	delete(environment, "Keys")
}

func normalizeVolumeTree(value any) {
	volumes, _ := value.(map[string]any)
	delete(volumes, "Map")
	sortJSONList(volumes, "Volumes")
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
	sortJSONList(node, key)
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
	if key == ConfigsField && (short || target == "") {
		target = "/" + source
	} else if key == SecretsField {
		if target == "" || short {
			target = source
		}
		target = secretTarget(target)
	}
	ref["target"] = target
	delete(ref, "short_form")
	normalizeObjectOwnership(ref)
}

// normalizeInitJobObjectRef mirrors InitJobRunner: short-form references keep the
// parsed target, an empty secret target uses the source name, and a config without
// a target is attached without a file.
func normalizeInitJobObjectRef(ref map[string]any, key string) {
	source, _ := ref["source"].(string)
	target, _ := ref["target"].(string)
	if key == SecretsField {
		if target == "" {
			target = source
		}
		target = secretTarget(target)
	}
	ref["target"] = target
	delete(ref, "short_form")
	if key == ConfigsField && target == "" {
		delete(ref, "target")
		delete(ref, "uid")
		delete(ref, "gid")
		delete(ref, "mode")
		return
	}
	normalizeObjectOwnership(ref)
}

// secretTarget returns the absolute path where Docker mounts a secret target.
func secretTarget(target string) string {
	if strings.HasPrefix(target, "/") {
		return target
	}
	return dockerSecretsDir + target
}

func normalizeObjectOwnership(ref map[string]any) {
	for _, owner := range []string{"uid", "gid"} {
		if ref[owner] == nil || ref[owner] == "" || ref[owner] == "0" {
			delete(ref, owner)
		}
	}
	if ref["mode"] == nil || ref["mode"] == jsonv1.Number("292") { // Docker's default file mode, 0444.
		delete(ref, "mode")
	}
}

func sortJSONList(node map[string]any, key string) {
	list, ok := node[key].([]any)
	if !ok {
		return
	}
	sort.SliceStable(list, func(i, j int) bool {
		left, _ := json.Marshal(list[i], jsonv1.DefaultOptionsV1())
		right, _ := json.Marshal(list[j], jsonv1.DefaultOptionsV1())
		return bytes.Compare(left, right) < 0
	})
}
