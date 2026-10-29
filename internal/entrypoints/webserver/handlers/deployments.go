package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
)

// GetDeploymentFilters returns stack and service names with recorded desired-state deployments.
func (h *handler) GetDeploymentFilters(ctx context.Context) (*generated.DeploymentFilterOptions, error) {
	stacks, services, err := h.deployments.FilterOptions(ctx)
	if err != nil {
		return nil, err
	}
	result := &generated.DeploymentFilterOptions{
		Stacks:   stacks,
		Services: make([]generated.DeploymentFilterService, 0, len(services)),
	}
	for _, service := range services {
		result.Services = append(result.Services, generated.DeploymentFilterService{
			StackName: service.StackName, ServiceName: service.ServiceName,
		})
	}
	return result, nil
}

func (h *handler) ListDeployments(
	ctx context.Context, params generated.ListDeploymentsParams,
) (*generated.DeploymentsResponse, error) {
	page, err := h.deployments.List(ctx, deploymentmodel.ListFilter{
		Stacks: params.Stack, Services: params.ServiceName,
		Limit: int(params.Limit.Or(0)), Cursor: params.Cursor.Or(""),
	})
	if errors.Is(err, deploymentstore.ErrInvalidCursor) {
		return nil, withStatusError(http.StatusBadRequest, err)
	}
	if err != nil {
		return nil, err
	}
	result := &generated.DeploymentsResponse{
		Deployments: make([]generated.DeploymentSummary, 0, len(page.Deployments)),
	}
	for _, item := range page.Deployments {
		result.Deployments = append(result.Deployments, toGeneratedDeploymentSummary(item))
	}
	if page.NextCursor != "" {
		result.NextCursor = generated.NewOptString(page.NextCursor)
	}
	return result, nil
}

func (h *handler) GetDeployment(
	ctx context.Context, params generated.GetDeploymentParams,
) (*generated.Deployment, error) {
	item, err := h.deployments.Get(ctx, params.ID)
	if errors.Is(err, deploymentstore.ErrNotFound) {
		return nil, withStatusError(http.StatusNotFound, err)
	}
	if err != nil {
		return nil, err
	}
	result := toGeneratedDeployment(item)
	desired, err := h.deployments.Desired(ctx, params.ID)
	if err != nil {
		return nil, err
	}
	result.Services = make([]generated.DeploymentService, 0, len(desired.Compose.Services))
	for _, service := range desired.Compose.Services {
		result.Services = append(result.Services, generated.DeploymentService{
			Name: service.Name, Image: service.Image, Replicas: deploymentServiceReplicas(service.Deploy),
		})
	}
	configuration, err := desired.MarshalYAML()
	if err != nil {
		return nil, err
	}
	result.ConfigurationYaml = generated.NewOptString(string(configuration))
	if item.ErrorDiagnostic != "" {
		result.Diagnostic = generated.NewOptString(item.ErrorDiagnostic)
	}
	return &result, nil
}

// GetDeploymentInitJobLogs writes exact stored Docker output for one validated execution relationship.
func (h *handler) GetDeploymentInitJobLogs(
	ctx context.Context,
	params generated.GetDeploymentInitJobLogsParams,
	w http.ResponseWriter,
) error {
	logs, err := h.deployments.InitJobLogs(ctx, params.ID, params.AttemptID, params.Ordinal)
	if errors.Is(err, deploymentstore.ErrNotFound) || errors.Is(err, deploymentstore.ErrLogsUnavailable) {
		return withStatusError(http.StatusNotFound, err)
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(logs); err != nil {
		return fmt.Errorf("write init job logs: %w", err)
	}
	return nil
}

func deploymentServiceReplicas(deploy compose.ServiceDeploy) string {
	switch deploy.Mode {
	case "", "replicated":
		if deploy.Replicas == nil {
			return "1"
		}
		return strconv.FormatUint(*deploy.Replicas, 10)
	case "global":
		return "Global"
	case "global-job":
		return "Global job"
	case "replicated-job":
		parts := []string{}
		for _, field := range []struct{ key, label string }{
			{"max_concurrent", "concurrent"}, {"total_completions", "total"},
		} {
			if count := jobCount(deploy.Extra[field.key]); count != "" {
				parts = append(parts, count+" "+field.label)
			}
		}
		if len(parts) == 0 {
			return "Replicated job"
		}
		return "Job: " + strings.Join(parts, ", ")
	default:
		return "—"
	}
}

func jobCount(value any) string {
	switch count := value.(type) {
	case int:
		if count >= 0 {
			return strconv.Itoa(count)
		}
	case uint64:
		return strconv.FormatUint(count, 10)
	case float64:
		if count >= 0 && count == float64(uint64(count)) {
			return strconv.FormatUint(uint64(count), 10)
		}
	}
	return ""
}

func toGeneratedDeploymentSummary(item deploymentmodel.DeploymentSummary) generated.DeploymentSummary {
	result := generated.DeploymentSummary{
		ID: item.ID, Stack: item.Stack, Commit: item.Commit,
		Status:             generated.DeploymentStatus(item.Status),
		Phase:              generated.DeploymentPhase(item.Phase),
		ApplyStatus:        generated.DeploymentStageStatus(item.ApplyStatus),
		VerificationStatus: generated.DeploymentStageStatus(item.VerificationStatus),
		CleanupStatus:      generated.DeploymentStageStatus(item.CleanupStatus),
		ActualStateStatus:  generated.DeploymentActualStateStatus(item.ActualStateStatus),
		ComparisonBasis:    generated.DeploymentComparisonBasis(item.ComparisonBasis),
		ComparisonStatus:   generated.DeploymentComparisonStatus(item.ComparisonStatus),
		StartedAt:          item.StartedAt,
		Summary: generated.DeploymentChangeSummary{
			Added: item.Summary.Added, Changed: item.Summary.Changed,
			Removed: item.Summary.Removed, Redacted: item.Summary.Redacted,
		},
		Resources: generated.DeploymentResourceSummary{
			Services: item.Resources.Services, Configs: item.Resources.Configs, Secrets: item.Resources.Secrets,
			Networks: item.Resources.Networks, Volumes: item.Resources.Volumes,
		},
		DiffAvailable:   item.DiffAvailable,
		LatestAttemptID: item.LatestAttemptID,
		AttemptCount:    item.AttemptCount,
	}
	setGeneratedDeploymentSummaryOptional(&result, item.ErrorCode, item.BasisDeploymentID,
		item.StartedAt, item.FinishedAt, item.ObservedAt)
	return result
}

func toGeneratedDeployment(item deploymentmodel.Deployment) generated.Deployment {
	result := generated.Deployment{
		ID: item.ID, Stack: item.Stack, Commit: item.Commit,
		Status:             generated.DeploymentStatus(item.Status),
		Phase:              generated.DeploymentPhase(item.Phase),
		ApplyStatus:        generated.DeploymentStageStatus(item.ApplyStatus),
		VerificationStatus: generated.DeploymentStageStatus(item.VerificationStatus),
		CleanupStatus:      generated.DeploymentStageStatus(item.CleanupStatus),
		ActualStateStatus:  generated.DeploymentActualStateStatus(item.ActualStateStatus),
		ComparisonBasis:    generated.DeploymentComparisonBasis(item.ComparisonBasis),
		ComparisonStatus:   generated.DeploymentComparisonStatus(item.ComparisonStatus),
		StartedAt:          item.StartedAt,
		Summary: generated.DeploymentChangeSummary{
			Added: item.Summary.Added, Changed: item.Summary.Changed,
			Removed: item.Summary.Removed, Redacted: item.Summary.Redacted,
		},
		Resources: generated.DeploymentResourceSummary{
			Services: item.Resources.Services, Configs: item.Resources.Configs, Secrets: item.Resources.Secrets,
			Networks: item.Resources.Networks, Volumes: item.Resources.Volumes,
		},
		DiffAvailable:   item.DiffAvailable,
		LatestAttemptID: item.LatestAttemptID,
		AttemptCount:    item.AttemptCount,
		Changes:         make([]generated.DeploymentChange, 0, len(item.Changes)),
		Attempts:        make([]generated.DeploymentAttempt, 0, len(item.Attempts)),
	}
	if item.ErrorCode != "" {
		result.Reason = generated.NewOptString(item.ErrorCode)
	}
	if item.BasisDeploymentID != "" {
		result.BasisDeploymentID = generated.NewOptString(item.BasisDeploymentID)
	}
	setGeneratedTimes(item.StartedAt, item.FinishedAt, item.ObservedAt,
		&result.FinishedAt, &result.ObservedAt, &result.DurationMs)
	for _, change := range item.Changes {
		mapped := generated.DeploymentChange{
			ResourceType: change.ResourceType, ResourceName: change.ResourceName, Field: change.Field,
			Operation: generated.DeploymentChangeOperation(change.Operation), Redacted: change.Redacted,
		}
		if change.Before != nil {
			mapped.Before = generated.NewOptString(*change.Before)
		}
		if change.After != nil {
			mapped.After = generated.NewOptString(*change.After)
		}
		result.Changes = append(result.Changes, mapped)
	}
	for _, attempt := range item.Attempts {
		mapped := generated.DeploymentAttempt{
			ID: attempt.ID, Number: attempt.Number, Status: generated.DeploymentAttemptStatus(attempt.Status),
			Phase:              generated.DeploymentPhase(attempt.Phase),
			ApplyStatus:        generated.DeploymentStageStatus(attempt.ApplyStatus),
			VerificationStatus: generated.DeploymentStageStatus(attempt.VerificationStatus),
			CleanupStatus:      generated.DeploymentStageStatus(attempt.CleanupStatus),
			ActualStateStatus:  generated.DeploymentActualStateStatus(attempt.ActualStateStatus),
			StartedAt:          attempt.StartedAt,
			InitJobs:           make([]generated.DeploymentInitJobExecution, 0, len(attempt.InitJobs)),
		}
		for _, execution := range attempt.InitJobs {
			mapped.InitJobs = append(mapped.InitJobs, generated.DeploymentInitJobExecution{
				Ordinal: execution.Ordinal, ServiceName: execution.ServiceName, JobName: execution.JobName,
				Status:    generated.DeploymentInitJobExecutionStatus(execution.Status),
				StartedAt: execution.StartedAt, FinishedAt: execution.FinishedAt,
				LogsAvailable: execution.LogsAvailable, LogsTruncated: execution.LogsTruncated,
			})
		}
		if attempt.ErrorCode != "" {
			mapped.Reason = generated.NewOptString(attempt.ErrorCode)
		}
		if attempt.ErrorDiagnostic != "" {
			mapped.Diagnostic = generated.NewOptString(attempt.ErrorDiagnostic)
		}
		setGeneratedTimes(attempt.StartedAt, attempt.FinishedAt, attempt.ObservedAt,
			&mapped.FinishedAt, &mapped.ObservedAt, &mapped.DurationMs)
		result.Attempts = append(result.Attempts, mapped)
	}
	return result
}

func setGeneratedDeploymentSummaryOptional(
	result *generated.DeploymentSummary,
	reason string,
	basisID string,
	started time.Time,
	finished *time.Time,
	observed *time.Time,
) {
	if reason != "" {
		result.Reason = generated.NewOptString(reason)
	}
	if basisID != "" {
		result.BasisDeploymentID = generated.NewOptString(basisID)
	}
	setGeneratedTimes(started, finished, observed, &result.FinishedAt, &result.ObservedAt, &result.DurationMs)
}

func setGeneratedTimes(
	started time.Time,
	finished *time.Time,
	observed *time.Time,
	finishedResult *generated.OptDateTime,
	observedResult *generated.OptDateTime,
	durationResult *generated.OptInt64,
) {
	if finished != nil {
		*finishedResult = generated.NewOptDateTime(*finished)
		duration := finished.Sub(started).Milliseconds()
		if duration < 0 {
			duration = 0
		}
		*durationResult = generated.NewOptInt64(duration)
	}
	if observed != nil {
		*observedResult = generated.NewOptDateTime(*observed)
	}
}
