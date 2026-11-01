package model

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

const (
	ignoredOptionPathParts = 4
	configsField           = "configs"
	secretsField           = "secrets"
)

// comparisonCompose canonicalizes execution syntax without mutating the parser's input.
func comparisonCompose(desired compose.Compose) (compose.Compose, error) {
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
	}
	return desired, nil
}

func normalizeObjectRefs(node map[string]any, key string) {
	refs, _ := node[key].([]any)
	for _, value := range refs {
		ref, ok := value.(map[string]any)
		if !ok {
			continue
		}
		normalizeObjectRef(ref, key)
	}
}

func normalizeObjectRef(ref map[string]any, key string) {
	source, _ := ref["source"].(string)
	target, _ := ref["target"].(string)
	short, _ := ref["short_form"].(bool)
	if key == configsField && (short || target == "") {
		target = "/" + source
	}
	if key == secretsField {
		if target == "" || short {
			target = source
		}
		if !strings.HasPrefix(target, "/") {
			target = "/run/secrets/" + target
		}
	}
	ref["target"] = target
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

func normalizeIgnoredOptions(tree map[string]any) {
	services, _ := tree["services"].([]any)
	for _, value := range services {
		service, _ := value.(map[string]any)
		extra, _ := service["Extra"].(map[string]any)
		for option := range extra {
			if compose.IsSwarmIgnoredOption(option) {
				delete(extra, option)
			}
		}
		if len(extra) == 0 {
			service["Extra"] = nil
		}
	}
}

func ignoredComparisonPath(path string) bool {
	parts := strings.Split(path, "/")
	last := len(parts) - 1
	if last > 0 && parts[last] == "IsList" {
		switch parts[last-1] {
		case "command", "entrypoint", "test":
			return true
		}
	}
	if last > 1 && parts[last] == "short_form" && (parts[last-2] == secretsField || parts[last-2] == configsField) {
		return true
	}
	return len(parts) >= ignoredOptionPathParts && parts[0] == "services" && parts[2] == "Extra" &&
		compose.IsSwarmIgnoredOption(parts[3])
}
