package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
)

const (
	dailyActivityBuckets  = 30
	weeklyActivityBuckets = 8
	daysInWeek            = 7
)

// GetStackDeploymentActivity returns zero-filled deployment counts for one configured stack.
func (h *handler) GetStackDeploymentActivity(
	ctx context.Context,
	params generated.GetStackDeploymentActivityParams,
) (*generated.DeploymentActivityResponse, error) {
	if _, found := h.resolveStackComposeFile(params.Stack); !found {
		return nil, withStatusError(http.StatusNotFound, fmt.Errorf("stack %s not found", params.Stack))
	}
	interval := params.Interval.Or(generated.DeploymentActivityIntervalWeekly)
	storeInterval := deploymentstore.ActivityInterval(interval)
	start, end := deploymentActivityBounds(storeInterval, time.Now())
	buckets, err := h.deployments.Activity(ctx, params.Stack, storeInterval, start, end)
	if err != nil {
		return nil, fmt.Errorf("get stack deployment activity: %w", err)
	}
	result := &generated.DeploymentActivityResponse{
		Buckets: make([]generated.DeploymentActivityBucket, 0, len(buckets)),
	}
	for _, bucket := range buckets {
		result.Buckets = append(result.Buckets, generated.DeploymentActivityBucket{
			Start: bucket.Start, Succeeded: bucket.Succeeded, Failed: bucket.Failed,
			Running: bucket.Running, Interrupted: bucket.Interrupted, Superseded: bucket.Superseded,
		})
	}
	return result, nil
}

func deploymentActivityBounds(interval deploymentstore.ActivityInterval, now time.Time) (time.Time, time.Time) {
	day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if interval == deploymentstore.ActivityIntervalDaily {
		end := day.AddDate(0, 0, 1)
		return end.AddDate(0, 0, -dailyActivityBuckets), end
	}
	daysSinceMonday := (int(day.Weekday()) + daysInWeek - 1) % daysInWeek
	week := day.AddDate(0, 0, -daysSinceMonday)
	return week.AddDate(0, 0, -daysInWeek*(weeklyActivityBuckets-1)), week.AddDate(0, 0, daysInWeek)
}
