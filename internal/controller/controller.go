package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/gitops"
	"github.com/artarts36/swarm-deploy/internal/metrics"
	"github.com/artarts36/swarm-deploy/internal/notify"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

type TriggerReason string

const (
	TriggerStartup TriggerReason = "startup"
	TriggerPoll    TriggerReason = "poll"
	TriggerWebhook TriggerReason = "webhook"
	TriggerManual  TriggerReason = "manual"
)

type StackView struct {
	Name         string        `json:"name"`
	ComposeFile  string        `json:"compose_file"`
	LastStatus   string        `json:"last_status"`
	LastError    string        `json:"last_error,omitempty"`
	LastCommit   string        `json:"last_commit,omitempty"`
	LastDeployAt time.Time     `json:"last_deploy_at,omitempty"`
	SourceDigest string        `json:"source_digest,omitempty"`
	Services     []ServiceView `json:"services"`
}

type ServiceView struct {
	Name         string    `json:"name"`
	Image        string    `json:"image,omitempty"`
	ImageVersion string    `json:"image_version,omitempty"`
	LastStatus   string    `json:"last_status,omitempty"`
	LastDeployAt time.Time `json:"last_deploy_at,omitempty"`
}

type serviceState struct {
	Image        string
	LastStatus   string
	LastDeployAt time.Time
}

type stackState struct {
	SourceDigest string
	LastCommit   string
	LastStatus   string
	LastError    string
	LastDeployAt time.Time
	Services     map[string]serviceState
}

type runtimeState struct {
	LastSyncAt     time.Time
	LastSyncReason string
	LastSyncResult string
	LastSyncError  string
	GitRevision    string
	Stacks         map[string]stackState
}

type Controller struct {
	cfg      *config.Config
	gitSync  *gitops.Syncer
	deployer *swarm.Deployer
	metrics  *metrics.Recorder
	notify   *notify.Manager

	stateMu sync.RWMutex
	state   runtimeState

	triggerCh chan TriggerReason
}

func New(
	cfg *config.Config,
	gitSync *gitops.Syncer,
	deployer *swarm.Deployer,
	metricRecorder *metrics.Recorder,
	notifier *notify.Manager,
) *Controller {
	return &Controller{
		cfg:      cfg,
		gitSync:  gitSync,
		deployer: deployer,
		metrics:  metricRecorder,
		notify:   notifier,
		state: runtimeState{
			Stacks: map[string]stackState{},
		},
		triggerCh: make(chan TriggerReason, 1),
	}
}

func (c *Controller) Run(ctx context.Context) error {
	var ticker *time.Ticker
	if c.cfg.Spec.Sync.Mode == config.SyncModePull || c.cfg.Spec.Sync.Mode == config.SyncModeHybrid {
		ticker = time.NewTicker(c.cfg.Spec.Sync.PollInterval.Value)
		defer ticker.Stop()
	}

	c.Trigger(TriggerStartup)

	for {
		select {
		case <-ctx.Done():
			return nil
		case reason := <-c.triggerCh:
			c.syncOnce(ctx, reason)
		case <-tickerC(ticker):
			c.Trigger(TriggerPoll)
		}
	}
}

func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func (c *Controller) Trigger(reason TriggerReason) bool {
	select {
	case c.triggerCh <- reason:
		return true
	default:
		return false
	}
}

func (c *Controller) ListStacks() []StackView {
	snapshot := c.snapshotState()
	stacks := make([]StackView, 0, len(c.cfg.Spec.Stacks))

	for _, stackCfg := range c.cfg.Spec.Stacks {
		stackState, ok := snapshot.Stacks[stackCfg.Name]
		view := StackView{
			Name:         stackCfg.Name,
			ComposeFile:  stackCfg.ComposeFile,
			LastStatus:   "unknown",
			SourceDigest: stackState.SourceDigest,
			Services:     nil,
		}
		if ok {
			view.LastStatus = stackState.LastStatus
			view.LastError = stackState.LastError
			view.LastCommit = stackState.LastCommit
			view.LastDeployAt = stackState.LastDeployAt
		}

		serviceNames := make([]string, 0, len(stackState.Services))
		for serviceName := range stackState.Services {
			serviceNames = append(serviceNames, serviceName)
		}
		sort.Strings(serviceNames)

		for _, serviceName := range serviceNames {
			service := stackState.Services[serviceName]
			view.Services = append(view.Services, ServiceView{
				Name:         serviceName,
				Image:        service.Image,
				ImageVersion: compose.ImageVersion(service.Image),
				LastStatus:   service.LastStatus,
				LastDeployAt: service.LastDeployAt,
			})
		}

		stacks = append(stacks, view)
	}

	return stacks
}

func (c *Controller) syncOnce(ctx context.Context, reason TriggerReason) {
	startedAt := time.Now()

	syncResult, err := c.gitSync.Sync(ctx)
	if err != nil {
		slog.Error("sync failed at git stage",
			slog.String("reason", string(reason)),
			slog.String("repository", c.cfg.Spec.Git.Repository),
			slog.Any("err", err),
		)
		c.metrics.RecordGitUpdate(c.cfg.Spec.Git.Repository, "error")
		c.metrics.RecordSyncRun(string(reason), "error", time.Since(startedAt))
		c.updateState(func(s *runtimeState) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(reason)
			s.LastSyncResult = "error"
			s.LastSyncError = err.Error()
		})
		return
	}

	updateResult := "no_change"
	if syncResult.Updated {
		updateResult = "updated"
	}
	c.metrics.RecordGitUpdate(c.cfg.Spec.Git.Repository, updateResult)

	if !syncResult.Updated && reason != TriggerManual {
		c.metrics.RecordSyncRun(string(reason), "no_change", time.Since(startedAt))
		c.updateState(func(s *runtimeState) {
			s.LastSyncAt = time.Now()
			s.LastSyncReason = string(reason)
			s.LastSyncResult = "no_change"
			s.LastSyncError = ""
			s.GitRevision = syncResult.NewRevision
		})
		return
	}

	var deployErrs []error
	for _, stackCfg := range c.cfg.Spec.Stacks {
		if err := c.syncStack(ctx, stackCfg, syncResult.NewRevision); err != nil {
			deployErrs = append(deployErrs, err)
			slog.Error("sync failed for stack",
				slog.String("reason", string(reason)),
				slog.String("stack", stackCfg.Name),
				slog.String("commit", syncResult.NewRevision),
				slog.Any("err", err),
			)
		}
	}

	result := "success"
	combinedErr := errors.Join(deployErrs...)
	if combinedErr != nil {
		result = "partial_error"
		slog.Error("sync finished with errors",
			slog.String("reason", string(reason)),
			slog.String("commit", syncResult.NewRevision),
			slog.Any("err", combinedErr),
		)
	}

	c.metrics.RecordSyncRun(string(reason), result, time.Since(startedAt))
	c.updateState(func(s *runtimeState) {
		s.LastSyncAt = time.Now()
		s.LastSyncReason = string(reason)
		s.LastSyncResult = result
		s.LastSyncError = ""
		if combinedErr != nil {
			s.LastSyncError = combinedErr.Error()
		}
		s.GitRevision = syncResult.NewRevision
	})
}

func (c *Controller) syncStack(ctx context.Context, stackCfg config.StackSpec, commit string) error {
	composePath := filepath.Join(c.gitSync.WorkingDir(), stackCfg.ComposeFile)
	stackFile, err := compose.Load(composePath)
	if err != nil {
		c.recordStackFailure(stackCfg.Name, commit, nil, err)
		return fmt.Errorf("stack %s load compose: %w", stackCfg.Name, err)
	}

	digest, err := stackFile.ComputeDigest(composePath)
	if err != nil {
		c.recordStackFailure(stackCfg.Name, commit, stackFile.Services, err)
		return fmt.Errorf("stack %s compute digest: %w", stackCfg.Name, err)
	}

	currentState := c.snapshotState()
	prev, exists := currentState.Stacks[stackCfg.Name]
	if exists && prev.SourceDigest == digest {
		return nil
	}

	deployComposePath := composePath
	if c.cfg.Spec.SecretRotation.Enabled {
		if _, err := stackFile.ApplyObjectRotation(
			stackCfg.Name,
			composePath,
			c.cfg.Spec.SecretRotation.HashLength,
			c.cfg.Spec.SecretRotation.IncludePath,
		); err != nil {
			c.recordStackFailure(stackCfg.Name, commit, stackFile.Services, err)
			return fmt.Errorf("stack %s rotate objects: %w", stackCfg.Name, err)
		}

		renderedPath, err := c.writeRenderedCompose(stackCfg.Name, stackFile)
		if err != nil {
			c.recordStackFailure(stackCfg.Name, commit, stackFile.Services, err)
			return fmt.Errorf("stack %s write rendered compose: %w", stackCfg.Name, err)
		}
		deployComposePath = renderedPath
	}

	if err := c.runInitJobs(ctx, stackCfg.Name, stackFile.Services); err != nil {
		c.recordStackFailure(stackCfg.Name, commit, stackFile.Services, err)
		return fmt.Errorf("stack %s init jobs: %w", stackCfg.Name, err)
	}

	if err := c.deployer.DeployStack(ctx, stackCfg.Name, deployComposePath); err != nil {
		c.recordStackFailure(stackCfg.Name, commit, stackFile.Services, err)
		return fmt.Errorf("stack %s deploy: %w", stackCfg.Name, err)
	}

	now := time.Now()
	servicesState := map[string]serviceState{}
	for _, service := range stackFile.Services {
		servicesState[service.Name] = serviceState{
			Image:        service.Image,
			LastStatus:   "success",
			LastDeployAt: now,
		}
		c.metrics.RecordDeploy(stackCfg.Name, service.Name, "success")
	}

	c.updateState(func(s *runtimeState) {
		s.Stacks[stackCfg.Name] = stackState{
			SourceDigest: digest,
			LastCommit:   commit,
			LastStatus:   "success",
			LastError:    "",
			LastDeployAt: now,
			Services:     servicesState,
		}
	})

	c.dispatchDeployEvents("success", stackCfg.Name, commit, stackFile.Services, "")
	return nil
}

func (c *Controller) runInitJobs(ctx context.Context, stackName string, services []compose.Service) error {
	for _, service := range services {
		for _, job := range service.InitJobs {
			err := c.deployer.RunInitJob(ctx, swarm.InitJobSpec{
				StackName:      stackName,
				ServiceName:    service.Name,
				DefaultNetwork: service.Networks,
				ServiceSecrets: service.Secrets,
				ServiceConfigs: service.Configs,
				Job:            job,
			})
			if err != nil {
				return fmt.Errorf("service %s init job %s: %w", service.Name, job.Name, err)
			}
		}
	}
	return nil
}

func (c *Controller) writeRenderedCompose(stackName string, stackFile *compose.File) (string, error) {
	renderedDir := filepath.Join(c.cfg.Spec.DataDir, "rendered")
	if err := os.MkdirAll(renderedDir, 0o755); err != nil {
		return "", fmt.Errorf("create rendered dir: %w", err)
	}

	payload, err := stackFile.MarshalYAML()
	if err != nil {
		return "", err
	}

	target := filepath.Join(renderedDir, stackName+".yaml")
	if err := os.WriteFile(target, payload, 0o600); err != nil {
		return "", fmt.Errorf("write rendered compose %s: %w", target, err)
	}
	return target, nil
}

func (c *Controller) recordStackFailure(stackName, commit string, services []compose.Service, reason error) {
	now := time.Now()
	servicesState := map[string]serviceState{}
	for _, service := range services {
		servicesState[service.Name] = serviceState{
			Image:        service.Image,
			LastStatus:   "failed",
			LastDeployAt: now,
		}
		c.metrics.RecordDeploy(stackName, service.Name, "failed")
	}
	if len(servicesState) == 0 {
		c.metrics.RecordDeploy(stackName, "unknown", "failed")
	}

	c.updateState(func(s *runtimeState) {
		s.Stacks[stackName] = stackState{
			SourceDigest: "",
			LastCommit:   commit,
			LastStatus:   "failed",
			LastError:    reason.Error(),
			LastDeployAt: now,
			Services:     servicesState,
		}
	})

	c.dispatchDeployEvents("failed", stackName, commit, services, reason.Error())
}

func (c *Controller) dispatchDeployEvents(status, stackName, commit string, services []compose.Service, errorMessage string) {
	if len(services) == 0 {
		_ = c.notify.Notify(context.Background(), notify.Event{
			Status:    status,
			StackName: stackName,
			Service:   "unknown",
			Image: notify.Image{
				FullName: "unknown",
				Version:  "unknown",
			},
			Commit:    commit,
			Error:     errorMessage,
			Timestamp: time.Now(),
		})
		return
	}

	for _, service := range services {
		imageName := service.Image
		if imageName == "" {
			imageName = "unknown"
		}
		event := notify.Event{
			Status:    status,
			StackName: stackName,
			Service:   service.Name,
			Image: notify.Image{
				FullName: imageName,
				Version:  compose.ImageVersion(imageName),
			},
			Commit:    commit,
			Error:     errorMessage,
			Timestamp: time.Now(),
		}
		_ = c.notify.Notify(context.Background(), event)
	}
}

func (c *Controller) LastSyncInfo() map[string]string {
	s := c.snapshotState()
	info := map[string]string{
		"last_sync_reason": s.LastSyncReason,
		"last_sync_result": s.LastSyncResult,
		"last_sync_error":  strings.TrimSpace(s.LastSyncError),
		"git_revision":     s.GitRevision,
	}
	if !s.LastSyncAt.IsZero() {
		info["last_sync_at"] = s.LastSyncAt.Format(time.RFC3339)
	}
	return info
}

func (c *Controller) snapshotState() runtimeState {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	cloned := c.state
	cloned.Stacks = map[string]stackState{}
	for stackName, st := range c.state.Stacks {
		stackCopy := st
		stackCopy.Services = map[string]serviceState{}
		for serviceName, service := range st.Services {
			stackCopy.Services[serviceName] = service
		}
		cloned.Stacks[stackName] = stackCopy
	}

	return cloned
}

func (c *Controller) updateState(fn func(*runtimeState)) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	fn(&c.state)
	if c.state.Stacks == nil {
		c.state.Stacks = map[string]stackState{}
	}
}
