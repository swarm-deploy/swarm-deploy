package tools

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

const configGetToolName = string(config.AssistantToolNameSwarmDeployConfigGet)

var configSections = []string{
	"gitops", "deployments", "event", "system", "notifications", "web", "healthServer", "assistant", "hostings",
}

var errConfigGetArguments = errors.New("expected a JSON object with only an optional supported section")

type swarmDeployConfigGet struct {
	settings globalConfiguration
}

type configGetResponse struct {
	Scope   string `json:"scope"`
	Section string `json:"section,omitempty"`
	Config  any    `json:"config"`
}

func newSwarmDeployConfigGet(
	spec config.Spec, allowed map[string]struct{}, builtins []routing.Tool,
) *swarmDeployConfigGet {
	return &swarmDeployConfigGet{settings: projectGlobalConfiguration(spec, allowed, builtins)}
}

func (*swarmDeployConfigGet) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name: config.AssistantToolNameSwarmDeployConfigGet,
		Description: "Read the running swarm-deploy's global configuration. Omit section for an overview. " +
			"Returns configured behavior only; does not inspect stack or service overrides or runtime health.",
		ParametersJSONSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"section": map[string]any{"type": "string", "enum": slices.Clone(configSections)},
			},
		},
	}
}

// ParseConfigGetSection validates the raw arguments without including them in errors.
func ParseConfigGetSection(arguments string) (string, error) {
	arguments = strings.TrimSpace(arguments)
	if !strings.HasPrefix(arguments, "{") {
		return "", errConfigGetArguments
	}
	var request struct {
		Section jsontext.Value `json:"section"`
	}
	if err := json.Unmarshal([]byte(arguments), &request, json.RejectUnknownMembers(true)); err != nil {
		return "", errConfigGetArguments
	}
	if len(request.Section) == 0 {
		return "", nil
	}
	var section string
	if err := json.Unmarshal(request.Section, &section); err != nil || !slices.Contains(configSections, section) {
		return "", errConfigGetArguments
	}
	return section, nil
}

func (t *swarmDeployConfigGet) Execute(_ context.Context, req routing.Request) (routing.Response, error) {
	arguments, ok := req.Payload.(string)
	if !ok {
		return routing.Response{}, errConfigGetArguments
	}
	section, err := ParseConfigGetSection(arguments)
	if err != nil {
		return routing.Response{}, err
	}
	return routing.Response{Payload: configGetResponse{
		Scope: "global", Section: section, Config: t.settings.section(section),
	}}, nil
}
