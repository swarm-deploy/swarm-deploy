package statem

import "time"

type Service struct {
	// Image is the deployed image reference for the service.
	Image string `json:"image"`
	// LastStatus is the latest deployment status for the service.
	LastStatus string `json:"last_status"`
	// LastDeployAt is the timestamp of the latest service deployment attempt.
	LastDeployAt time.Time `json:"last_deploy_at"`
}

type Stack struct {
	// SourceDigest is the digest of the source compose file used for deployment.
	SourceDigest string `json:"source_digest"`
	// LastCommit is the git commit hash that triggered the latest stack deployment.
	LastCommit string `json:"last_commit"`
	// LastStatus is the latest deployment status for the stack.
	LastStatus string `json:"last_status"`
	// LastError contains the latest deployment error message for the stack.
	LastError string `json:"last_error"`
	// LastDeployAt is the timestamp of the latest stack deployment attempt.
	LastDeployAt time.Time `json:"last_deploy_at"`
	// Services stores per-service deployment state for the stack.
	Services map[string]Service `json:"services"`
}

type Network struct {
	// Driver is a Docker network driver from desired network specification.
	Driver string `json:"driver"`
	// LastCommit is the git commit hash that triggered the latest network reconciliation.
	LastCommit string `json:"last_commit"`
	// LastStatus is the latest reconciliation status for the network.
	LastStatus string `json:"last_status"`
	// LastError contains the latest reconciliation error message for the network.
	LastError string `json:"last_error"`
	// LastSyncAt is the timestamp of the latest network reconciliation attempt.
	LastSyncAt time.Time `json:"last_sync_at"`
}

type Runtime struct {
	// LastSyncAt is the timestamp of the latest Sync attempt.
	LastSyncAt time.Time `json:"last_sync_at"`
	// LastSyncReason describes why the latest Sync was triggered.
	LastSyncReason string `json:"last_sync_reason"`
	// LastSyncResult stores the outcome of the latest Sync attempt.
	LastSyncResult string `json:"last_sync_result"`
	// LastSyncError contains the latest Sync error message.
	LastSyncError string `json:"last_sync_error"`
	// GitRevision is the git revision observed during the latest Sync.
	GitRevision string `json:"git_revision"`
	// Stacks stores deployment state keyed by stack name.
	Stacks map[string]Stack `json:"stacks"`
	// Networks stores network reconciliation state keyed by network name.
	Networks map[string]Network `json:"networks"`
}

func cloneRuntime(state Runtime) Runtime {
	cloned := state
	cloned.Stacks = map[string]Stack{}
	for stackName, stack := range state.Stacks {
		stackCopy := stack
		stackCopy.Services = map[string]Service{}
		for serviceName, service := range stack.Services {
			stackCopy.Services[serviceName] = service
		}
		cloned.Stacks[stackName] = stackCopy
	}
	cloned.Networks = map[string]Network{}
	for networkName, network := range state.Networks {
		cloned.Networks[networkName] = network
	}

	return cloned
}
