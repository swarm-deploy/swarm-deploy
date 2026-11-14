package compose_test

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/compose/composetest"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/utils"
)

// TestCanonicalizeMatchesReference guards persisted desired-state digests: the
// canonical form must stay byte-for-byte equal to the string-keyed implementation
// released before typed normalization.
func TestCanonicalizeMatchesReference(t *testing.T) {
	for _, fixture := range composetest.CanonicalFixtures() {
		t.Run(fixture.Name, func(t *testing.T) {
			file, err := fixture.Load()
			require.NoError(t, err)
			expected, err := referenceCanonicalize(file.Compose)
			require.NoError(t, err)

			actual, err := compose.Canonicalize(file.Compose)
			require.NoError(t, err)

			assert.Equal(t, string(expected), string(actual))
		})
	}
}

// referenceCanonicalize is a verbatim copy of compose.Canonicalize as released
// with string-keyed normalization of the JSON tree.
func referenceCanonicalize(desired compose.Compose) ([]byte, error) {
	normalized, err := referenceCanonicalizeServiceSyntax(desired)
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
	referenceNormalizeEffectiveTree(tree)
	canonical, err := json.Marshal(tree, jsonv1.DefaultOptionsV1())
	if err != nil {
		return nil, fmt.Errorf("encode canonical Compose: %w", err)
	}
	return canonical, nil
}

func referenceCanonicalizeServiceSyntax(desired compose.Compose) (compose.Compose, error) {
	desired.Services = slices.Clone(desired.Services)
	for i := range desired.Services {
		service := &desired.Services[i]
		for _, field := range []struct {
			name    string
			command *compose.Command
		}{{"command", &service.Command}, {"entrypoint", &service.Entrypoint}} {
			words, err := field.command.Words()
			if err != nil {
				return desired, fmt.Errorf("services.%s.%s: invalid quoting", service.Name, field.name)
			}
			if len(words) == 0 {
				words = nil
			}
			*field.command = compose.NewCommand(words)
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
		service.CapAdd, service.CapDrop = compose.NormalizeCapabilities(service.CapAdd, service.CapDrop)
	}
	return desired, nil
}

func referenceNormalizeEffectiveTree(tree map[string]any) {
	services, _ := tree["services"].([]any)
	for _, value := range services {
		service, ok := value.(map[string]any)
		if !ok {
			continue
		}
		referenceNormalizeServiceTree(service)
	}
	sort.SliceStable(services, func(i, j int) bool {
		left, _ := services[i].(map[string]any)
		right, _ := services[j].(map[string]any)
		return fmt.Sprint(left["name"]) < fmt.Sprint(right["name"])
	})
}

func referenceNormalizeServiceTree(service map[string]any) {
	delete(service, "env_file")
	referenceNormalizeEnvironmentTree(service["environment"])
	referenceNormalizeVolumeTree(service["volumes"])
	referenceNormalizeNetworkTree(service["networks"])
	referenceNormalizeSortedWrapperList(service["ports"], "Ports")
	referenceNormalizeObjectRefs(service, compose.SecretsField, false)
	referenceNormalizeObjectRefs(service, compose.ConfigsField, false)
	referenceSortJSONList(service, "cap_add")
	referenceSortJSONList(service, "cap_drop")

	initJobs, _ := service["init_jobs"].([]any)
	for _, value := range initJobs {
		job, ok := value.(map[string]any)
		if !ok {
			continue
		}
		referenceNormalizeEnvironmentTree(job["environment"])
		referenceNormalizeNetworkTree(job["networks"])
		referenceNormalizeObjectRefs(job, compose.SecretsField, true)
		referenceNormalizeObjectRefs(job, compose.ConfigsField, true)
	}
}

func referenceNormalizeEnvironmentTree(value any) {
	environment, _ := value.(map[string]any)
	delete(environment, "Keys")
}

func referenceNormalizeVolumeTree(value any) {
	volumes, _ := value.(map[string]any)
	delete(volumes, "Map")
	referenceSortJSONList(volumes, "Volumes")
}

func referenceNormalizeNetworkTree(value any) {
	networks, _ := value.(map[string]any)
	delete(networks, "AliasMap")
	delete(networks, "Names")
	delete(networks, "Aliases")
	referenceSortJSONList(networks, "List")
}

func referenceNormalizeSortedWrapperList(value any, key string) {
	wrapper, _ := value.(map[string]any)
	referenceSortJSONList(wrapper, key)
}

func referenceNormalizeObjectRefs(node map[string]any, key string, initJob bool) {
	refs, _ := node[key].([]any)
	for _, value := range refs {
		ref, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if initJob {
			referenceNormalizeInitJobObjectRef(ref, key)
		} else {
			referenceNormalizeServiceObjectRef(ref, key)
		}
	}
	referenceSortJSONList(node, key)
}

func referenceNormalizeServiceObjectRef(ref map[string]any, key string) {
	source, _ := ref["source"].(string)
	target, _ := ref["target"].(string)
	short, _ := ref["short_form"].(bool)
	if key == compose.ConfigsField && (short || target == "") {
		target = "/" + source
	} else if key == compose.SecretsField {
		if target == "" || short {
			target = source
		}
		target = referenceSecretTarget(target)
	}
	ref["target"] = target
	delete(ref, "short_form")
	referenceNormalizeObjectOwnership(ref)
}

func referenceNormalizeInitJobObjectRef(ref map[string]any, key string) {
	source, _ := ref["source"].(string)
	target, _ := ref["target"].(string)
	if key == compose.SecretsField {
		if target == "" {
			target = source
		}
		target = referenceSecretTarget(target)
	}
	ref["target"] = target
	delete(ref, "short_form")
	if key == compose.ConfigsField && target == "" {
		delete(ref, "target")
		delete(ref, "uid")
		delete(ref, "gid")
		delete(ref, "mode")
		return
	}
	referenceNormalizeObjectOwnership(ref)
}

func referenceSecretTarget(target string) string {
	if strings.HasPrefix(target, "/") {
		return target
	}
	return "/run/secrets/" + target
}

func referenceNormalizeObjectOwnership(ref map[string]any) {
	for _, owner := range []string{"uid", "gid"} {
		if ref[owner] == nil || ref[owner] == "" || ref[owner] == "0" {
			delete(ref, owner)
		}
	}
	if ref["mode"] == nil || ref["mode"] == jsonv1.Number("292") {
		delete(ref, "mode")
	}
}

func referenceSortJSONList(node map[string]any, key string) {
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
