package initjob

import (
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
)

// Metrics records Init Job executions.
type Metrics interface {
	RecordInitJobRun(stack, service string)
}

// Status describes the outcome of one Init Job execution.
type Status string

const (
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Interrupted Status = "interrupted"
)

// Result contains the observable result of one Init Job execution.
type Result struct {
	ServiceName   string
	JobName       string
	Status        Status
	StartedAt     time.Time
	FinishedAt    time.Time
	Logs          []byte
	LogsAvailable bool
	LogsTruncated bool
}

// Spec contains the deployment context required to execute one Init Job.
type Spec struct {
	StackName      string
	ServiceName    string
	DefaultNetwork []string
	// NetworkTargets maps Compose network aliases to reconciled Docker network IDs.
	NetworkTargets  map[string]string
	ServiceSecrets  []compose.ObjectRef
	ServiceConfigs  []compose.ObjectRef
	ResolvedSecrets map[string]resources.ResolvedResource
	ResolvedConfigs map[string]resources.ResolvedResource
	Job             compose.InitJob
}

// Compatibility names keep the established deployment vocabulary available to callers.
type (
	InitJobMetrics = Metrics
	InitJobStatus  = Status
	InitJobResult  = Result
	InitJobSpec    = Spec
)

const (
	InitJobSucceeded   = Succeeded
	InitJobFailed      = Failed
	InitJobInterrupted = Interrupted
)
