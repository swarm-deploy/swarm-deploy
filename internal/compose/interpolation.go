package compose

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	interp "github.com/docker/cli/cli/compose/interpolation"
	"github.com/docker/cli/cli/compose/template"
	"go.yaml.in/yaml/v3"
)

// interpolateSyntax processes source YAML values exactly once, before decoding
// models or reading env/config/secret payloads. Keys are never interpolated.
// Use the stack CLI's template engine, not the newer Compose expansion language.
func interpolateSyntax(syntax *yaml.Node) (bool, error) {
	var values map[string]any
	if err := syntax.Decode(&values); err != nil {
		return false, err
	}
	jobs := detachInitJobs(values)
	changed := false
	values, err := interp.Interpolate(values, interp.Options{
		LookupValue:     os.LookupEnv,
		TypeCastMapping: interpolationCasts,
		Substitute: func(value string, mapping template.Mapping) (string, error) {
			changed = changed || strings.Contains(value, "$")
			result, err := template.Substitute(value, mapping)
			if err != nil {
				// Upstream errors include the input and required-variable message.
				// Neither is safe to retain in validation errors or span events.
				return "", fmt.Errorf("invalid interpolation or missing required variable")
			}
			return result, nil
		},
	})
	if err != nil {
		return changed, err
	}
	if !changed {
		return false, nil
	}
	services, _ := values["services"].(map[string]any)
	for name, job := range jobs {
		service, _ := services[name].(map[string]any)
		service["x-init-deploy-jobs"] = job
	}
	return true, updateInterpolatedValues(syntax, values)
}

// Init Jobs are a controller extension executed directly, never by the stack
// CLI. Retain their existing literal shell/environment semantics.
func detachInitJobs(values map[string]any) map[string]any {
	jobs := make(map[string]any)
	services, _ := values["services"].(map[string]any)
	for name, value := range services {
		service, _ := value.(map[string]any)
		if job, exists := service["x-init-deploy-jobs"]; exists {
			jobs[name] = job
			delete(service, "x-init-deploy-jobs")
		}
	}
	return jobs
}

// Retain source mapping order (including service/Init Job execution order).
// Re-encoding the complete map would sort service names as a side effect.
func updateInterpolatedValues(node *yaml.Node, value any) error {
	switch node.Kind { //nolint:exhaustive // Other node kinds are encoded as leaf values.
	case yaml.DocumentNode:
		return updateInterpolatedValues(node.Content[0], value)
	case yaml.MappingNode:
		values, ok := value.(map[string]any)
		if !ok {
			return node.Encode(value)
		}
		return updateInterpolatedMapping(node, values)
	case yaml.SequenceNode:
		values, ok := value.([]any)
		if ok {
			for i, child := range node.Content {
				if err := updateInterpolatedValues(child, values[i]); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return node.Encode(value)
}

func updateInterpolatedMapping(node *yaml.Node, values map[string]any) error {
	for i := 0; i < len(node.Content); i += yamlMappingNodePairSize {
		if child, exists := values[node.Content[i].Value]; exists {
			if err := updateInterpolatedValues(node.Content[i+1], child); err != nil {
				return err
			}
		}
	}
	return nil
}

// These are the stack CLI's typed paths for fields supported by our models.
// Casting happens only after substitution, preserving literal environment values.
var interpolationCasts = map[interp.Path]interp.Cast{
	"services.*.configs.[].mode":                          toInterpolationInt,
	"services.*.secrets.[].mode":                          toInterpolationInt,
	"services.*.healthcheck.retries":                      toInterpolationInt,
	"services.*.healthcheck.disable":                      toInterpolationBool,
	"services.*.deploy.replicas":                          toInterpolationInt,
	"services.*.deploy.update_config.parallelism":         toInterpolationInt,
	"services.*.deploy.update_config.max_failure_ratio":   toInterpolationFloat,
	"services.*.deploy.rollback_config.parallelism":       toInterpolationInt,
	"services.*.deploy.rollback_config.max_failure_ratio": toInterpolationFloat,
	"services.*.deploy.restart_policy.max_attempts":       toInterpolationInt,
	"services.*.deploy.placement.max_replicas_per_node":   toInterpolationInt,
	"services.*.deploy.resources.limits.pids":             toInterpolationInt,
	"services.*.ports.[].target":                          toInterpolationInt,
	"services.*.ports.[].published":                       toInterpolationInt,
	"services.*.volumes.[].read_only":                     toInterpolationBool,
	"services.*.volumes.[].volume.nocopy":                 toInterpolationBool,
	"networks.*.external":                                 toInterpolationBool,
	"networks.*.internal":                                 toInterpolationBool,
	"networks.*.attachable":                               toInterpolationBool,
	"volumes.*.external":                                  toInterpolationBool,
	"secrets.*.external":                                  toInterpolationBool,
	"configs.*.external":                                  toInterpolationBool,
}

func toInterpolationInt(value string) (any, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf("invalid integer")
	}
	return parsed, nil
}

func toInterpolationFloat(value string) (any, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid number")
	}
	return parsed, nil
}

func toInterpolationBool(value string) (any, error) {
	switch strings.ToLower(value) {
	case "y", "yes", "true", "on":
		return true, nil
	case "n", "no", "false", "off":
		return false, nil
	default:
		return nil, fmt.Errorf("invalid boolean")
	}
}
