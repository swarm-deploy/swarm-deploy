package statem

import "time"

type Service struct {
	// Image is the deployed image reference for the service.
	Image string
	// LastStatus is the latest deployment status for the service.
	LastStatus string
	// LastDeployAt is the timestamp of the latest service deployment attempt.
	LastDeployAt time.Time
}

type Stack struct {
	// SourceDigest is the digest of the source compose file used for deployment.
	SourceDigest string
	// LastCommit is the git commit hash that triggered the latest stack deployment.
	LastCommit string
	// LastStatus is the latest deployment status for the stack.
	LastStatus string
	// LastError contains the latest deployment error message for the stack.
	LastError string
	// LastDeployAt is the timestamp of the latest stack deployment attempt.
	LastDeployAt time.Time
	// Services stores per-service deployment state for the stack.
	Services map[string]Service
}

type Runtime struct {
	// LastSyncAt is the timestamp of the latest sync attempt.
	LastSyncAt time.Time
	// LastSyncReason describes why the latest sync was triggered.
	LastSyncReason string
	// LastSyncResult stores the outcome of the latest sync attempt.
	LastSyncResult string
	// LastSyncError contains the latest sync error message.
	LastSyncError string
	// GitRevision is the git revision observed during the latest sync.
	GitRevision string
	// Stacks stores deployment state keyed by stack name.
	Stacks map[string]Stack
}
