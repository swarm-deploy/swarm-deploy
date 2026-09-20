package stackloop

import (
	"context"

	downward "github.com/swarm-deploy/downward/go"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

var downwardEnvironment = map[string]string{
	downward.EnvStackName:   "",
	downward.EnvServiceID:   "{{.Service.ID}}",
	downward.EnvServiceName: "{{.Service.Name}}",
	downward.EnvTaskID:      "{{.Task.ID}}",
	downward.EnvTaskName:    "{{.Task.Name}}",
	downward.EnvTaskSlot:    "{{.Task.Slot}}",
	downward.EnvNodeID:      "{{.Node.ID}}",
	downward.EnvNodeName:    "{{.Node.Hostname}}",
}

var downwardEnvironmentKeys = []string{
	downward.EnvStackName,
	downward.EnvServiceID,
	downward.EnvServiceName,
	downward.EnvTaskID,
	downward.EnvTaskName,
	downward.EnvTaskSlot,
	downward.EnvNodeID,
	downward.EnvNodeName,
}

func (r *Reconciler) addDownward(_ context.Context, payload *pipelinePayload) error {
	changed := false

	for index, service := range payload.Desired.Compose.Services {
		if serviceHasDownwardEnvironment(service.Environment) {
			continue
		}

		if service.Environment.Map == nil {
			service.Environment.Map = make(map[string]string, len(downwardEnvironment))
		}

		serviceChanged := false
		for key, value := range downwardEnvironment {
			if key == downward.EnvStackName {
				value = payload.Stack.Name
			}

			if service.Environment.Has(key) {
				continue
			}

			service.Environment.Map[key] = value
			serviceChanged = true
		}

		if !serviceChanged {
			continue
		}

		payload.Desired.Compose.Services[index] = service
		changed = true
	}

	if changed && payload.IsNewDigest {
		payload.DesiredMutated = true
	}

	return nil
}

func serviceHasDownwardEnvironment(environment compose.Environment) bool {
	for _, key := range downwardEnvironmentKeys {
		if environment.Has(key) {
			return true
		}
	}

	return false
}
