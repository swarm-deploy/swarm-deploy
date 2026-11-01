// Package services applies final converted service specifications through internal/swarm.
package services

import (
	"time"

	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

const (
	defaultStopGracePeriod = 10 * time.Second
	defaultRestartDelay    = 5 * time.Second
	defaultUpdateMonitor   = 5 * time.Second
)

func normalizeDockerDefaults(spec *dockerswarm.ServiceSpec) {
	if job := spec.Mode.ReplicatedJob; job != nil {
		if job.MaxConcurrent == nil {
			value := uint64(1)
			job.MaxConcurrent = &value
		}
		if job.TotalCompletions == nil {
			value := *job.MaxConcurrent
			job.TotalCompletions = &value
		}
	}
	if spec.Mode.Replicated != nil && spec.Mode.Replicated.Replicas == nil {
		replicas := uint64(1)
		spec.Mode.Replicated.Replicas = &replicas
	}
	if spec.EndpointSpec == nil {
		spec.EndpointSpec = &dockerswarm.EndpointSpec{}
	}
	if spec.EndpointSpec.Mode == "" {
		spec.EndpointSpec.Mode = dockerswarm.ResolutionModeVIP
	}
	if spec.TaskTemplate.ContainerSpec != nil {
		// Engine's SwarmKit conversion returns explicit runtime/isolation
		// defaults even when they were omitted in the create request.
		if spec.TaskTemplate.Runtime == "" {
			spec.TaskTemplate.Runtime = dockerswarm.RuntimeContainer
		}
		if spec.TaskTemplate.ContainerSpec.Isolation == "" {
			spec.TaskTemplate.ContainerSpec.Isolation = container.IsolationDefault
		}
		if spec.TaskTemplate.ContainerSpec.StopGracePeriod == nil {
			value := defaultStopGracePeriod
			spec.TaskTemplate.ContainerSpec.StopGracePeriod = &value
		}
	}
	if spec.TaskTemplate.RestartPolicy == nil {
		spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{}
	}
	restart := spec.TaskTemplate.RestartPolicy
	if restart.Condition == "" {
		restart.Condition = dockerswarm.RestartPolicyConditionAny
	}
	if restart.Delay == nil {
		value := defaultRestartDelay
		restart.Delay = &value
	}
	if restart.MaxAttempts == nil {
		value := uint64(0)
		restart.MaxAttempts = &value
	}
	spec.UpdateConfig = normalizeUpdateConfig(spec.UpdateConfig)
	spec.RollbackConfig = normalizeUpdateConfig(spec.RollbackConfig)
}

func normalizeUpdateConfig(config *dockerswarm.UpdateConfig) *dockerswarm.UpdateConfig {
	if config == nil {
		config = &dockerswarm.UpdateConfig{Parallelism: 1}
	}
	if config.FailureAction == "" {
		config.FailureAction = dockerswarm.UpdateFailureActionPause
	}
	if config.Monitor == 0 {
		config.Monitor = defaultUpdateMonitor
	}
	if config.Order == "" {
		config.Order = dockerswarm.UpdateOrderStopFirst
	}
	return config
}
