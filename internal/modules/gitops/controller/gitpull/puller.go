package gitpull

import (
	"context"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
)

const (
	updateResultError    = "error"
	updateResultNoChange = "no_change"
	updateResultUpdated  = "updated"
)

// Puller pulls the configured Git repository and records every pull outcome.
type Puller struct {
	git        gitx.Repository
	monitor    *Monitor
	metrics    metrics.Git
	remote     string
	authType   config.GitAuthType
	repository string
}

// NewPuller creates a Puller for the repository described by spec.
func NewPuller(git gitx.Repository, monitor *Monitor, gitMetrics metrics.Git, spec config.GitSpec) *Puller {
	return &Puller{
		git:        git,
		monitor:    monitor,
		metrics:    gitMetrics,
		remote:     spec.Repository,
		authType:   spec.Auth.Type,
		repository: gitx.RepositoryIdentifier(spec.Repository),
	}
}

// Pull fetches the repository. The returned error is classified and free of credentials.
// Failures to record the outcome are logged and never fail the pull.
func (p *Puller) Pull(ctx context.Context, reason string) (gitx.PullResult, error) {
	result, err := p.git.Pull(ctx)
	if err != nil {
		err = gitx.ClassifyPullError(err, p.remote, p.authType)
		category, diagnostic := gitx.PullFailure(err)
		slog.ErrorContext(ctx, "git pull failed",
			slog.String("reason", reason),
			slog.String("repository", p.repository),
			slog.String("category", string(category)),
			slog.String("diagnostic", diagnostic),
		)
		p.metrics.RecordGitUpdate(p.repository, updateResultError)
		p.observe(ctx, "persist Git pull failure observation", Observation{
			Repository:    p.repository,
			TriggerReason: reason,
			Failure:       &Failure{Category: category, Description: diagnostic},
		})

		return gitx.PullResult{}, err
	}

	p.observe(ctx, "persist Git pull recovery", Observation{
		Repository:    p.repository,
		TriggerReason: reason,
	})

	slog.InfoContext(ctx, "[controller] git synced", slog.Any("result", result))

	updateResult := updateResultNoChange
	if result.Updated {
		updateResult = updateResultUpdated
	}
	p.metrics.RecordGitUpdate(p.repository, updateResult)

	return result, nil
}

func (p *Puller) observe(ctx context.Context, failureMessage string, observation Observation) {
	if err := p.monitor.Observe(ctx, observation); err != nil {
		slog.ErrorContext(ctx, failureMessage,
			slog.String("repository", p.repository),
			slog.Any("err", err),
		)
	}
}
