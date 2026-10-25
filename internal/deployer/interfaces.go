//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mock.go -package=deployer

package deployer

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// Result contains observable results produced while deploying one stack.
type Result struct {
	// InitJobs contains completed Init Job executions in execution order.
	InitJobs []InitJobResult
}

// InitJobStatus describes the outcome of one Init Job execution.
type InitJobStatus string

const (
	// InitJobSucceeded means the Docker task completed successfully.
	InitJobSucceeded InitJobStatus = "succeeded"
	// InitJobFailed means the Docker task completed with a known failure.
	InitJobFailed InitJobStatus = "failed"
	// InitJobInterrupted means execution ended before a complete outcome was observed.
	InitJobInterrupted InitJobStatus = "interrupted"
)

// InitJobResult contains the observable result of one Init Job execution.
type InitJobResult struct {
	// ServiceName identifies the service declaring the Init Job.
	ServiceName string
	// JobName identifies the Init Job within the service.
	JobName string
	// Status is the observed execution outcome.
	Status InitJobStatus
	// StartedAt is recorded immediately before Docker service creation.
	StartedAt time.Time
	// FinishedAt is recorded when execution stops, before log collection.
	FinishedAt time.Time
	// Logs contains original bounded Docker output.
	Logs []byte
	// LogsAvailable distinguishes unavailable logs from successfully retrieved empty output.
	LogsAvailable bool
	// LogsTruncated reports that only the last bounded portion of output was retained.
	LogsTruncated bool
}

// StackDeployer reconciles one stack via deploy command execution.
type StackDeployer interface {
	// DeployStack reconciles init-job resources from sourceComposePath and applies deployComposePath.
	DeployStack(
		ctx context.Context,
		stackName,
		sourceComposePath,
		deployComposePath string,
		desired compose.Compose,
	) (Result, error)
}
