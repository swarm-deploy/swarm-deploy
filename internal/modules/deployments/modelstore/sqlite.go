// Package modelstore persists deployment models in SQLite.
package modelstore

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// ErrNotFound means the requested deployment, attempt or successful baseline does not exist.
var ErrNotFound = errors.New("deployment not found")

// ErrNotRunning prevents overwriting a terminal deployment outcome.
var ErrNotRunning = errors.New("deployment is not running")

// ErrInvalidCursor means a list cursor is malformed or unsupported.
var ErrInvalidCursor = errors.New("invalid deployment cursor")

// ErrLogsUnavailable means an Init Job execution exists but its Docker logs could not be collected.
var ErrLogsUnavailable = errors.New("init job logs unavailable")

// Store provides context-aware deployment and snapshot queries.
type Store struct{ db storage.Database }

// NewStore creates a deployment repository on the application database.
func NewStore(db storage.Database) *Store { return &Store{db: db} }

// FilterOptions returns stack and service names represented by desired-state deployments.
func (s *Store) FilterOptions(ctx context.Context) ([]string, []FilterService, error) {
	var rows []struct {
		Stack string `db:"stack"`
	}
	const stacksQuery = `SELECT DISTINCT stack FROM deployments WHERE stack <> '' ORDER BY stack`
	if err := s.db.Select(ctx, &rows, stacksQuery); err != nil {
		return nil, nil, err
	}
	stacks := make([]string, 0, len(rows))
	for _, row := range rows {
		stacks = append(stacks, row.Stack)
	}
	var services []FilterService
	if err := s.db.Select(ctx, &services, `SELECT DISTINCT d.stack AS stack_name,
		json_extract(svc.value, '$.name') AS service_name
		FROM deployments AS d, json_each(d.desired_payload, '$.definition.compose.services') AS svc
		WHERE json_extract(svc.value, '$.name') <> ''
		ORDER BY stack_name, service_name`); err != nil {
		return nil, nil, err
	}
	if services == nil {
		services = []FilterService{}
	}
	return stacks, services, nil
}

// FilterService identifies a service within a stack for deployment filtering.
type FilterService struct {
	// StackName identifies the stack containing the service.
	StackName string `db:"stack_name"`
	// ServiceName identifies the service in the recorded desired configuration.
	ServiceName string `db:"service_name"`
}

// ActivityInterval identifies the UTC bucket size used for deployment activity.
type ActivityInterval string

const (
	activityDaysPerWeek = 7
	// ActivityIntervalDaily groups deployment starts by UTC calendar day.
	ActivityIntervalDaily ActivityInterval = "daily"
	// ActivityIntervalWeekly groups deployment starts by Monday-based UTC calendar week.
	ActivityIntervalWeekly ActivityInterval = "weekly"
)

// ActivityBucket contains final deployment status counts for one UTC period.
type ActivityBucket struct {
	// Start is the inclusive UTC bucket boundary.
	Start time.Time
	// Succeeded is the number of successful deployments.
	Succeeded int
	// Failed is the number of failed deployments that have not been superseded.
	Failed int
	// Running is the number of currently running deployments.
	Running int
	// Interrupted is the number of interrupted deployments that have not been superseded.
	Interrupted int
	// Superseded is the number of failed or interrupted deployments replaced by newer desired state.
	Superseded int
}

// Activity returns chronological, zero-filled deployment status counts for [start, end).
func (s *Store) Activity(
	ctx context.Context,
	stack string,
	interval ActivityInterval,
	start time.Time,
	end time.Time,
) ([]ActivityBucket, error) {
	if interval != ActivityIntervalDaily && interval != ActivityIntervalWeekly {
		return nil, fmt.Errorf("unsupported deployment activity interval %q", interval)
	}
	start, end = start.UTC(), end.UTC()
	if !start.Before(end) {
		return nil, fmt.Errorf("deployment activity start must be before end")
	}

	dayExpression := `date(deployments.started_at / 1000000000, 'unixepoch')`
	if interval == ActivityIntervalWeekly {
		dayExpression = `date(deployments.started_at / 1000000000, 'unixepoch', printf('-%d days',
			(CAST(strftime('%w', deployments.started_at / 1000000000, 'unixepoch') AS INTEGER) + 6) % 7))`
	}
	query := `SELECT ` + dayExpression + ` AS bucket_date,
		CASE WHEN ` + supersededPredicate + ` THEN 'superseded' ELSE deployments.status END AS status,
		COUNT(*) AS count
		FROM deployments
		WHERE deployments.stack = ? AND deployments.started_at >= ? AND deployments.started_at < ?
		GROUP BY bucket_date, status
		ORDER BY bucket_date, status`
	var rows []struct {
		BucketDate string                 `db:"bucket_date"`
		Status     deploymentmodel.Status `db:"status"`
		Count      int                    `db:"count"`
	}
	if err := s.db.Select(ctx, &rows, query, stack, start.UnixNano(), end.UnixNano()); err != nil {
		return nil, err
	}

	buckets := make([]ActivityBucket, 0)
	byStart := map[time.Time]int{}
	for bucketStart := start; bucketStart.Before(end); bucketStart = nextActivityBucket(bucketStart, interval) {
		byStart[bucketStart] = len(buckets)
		buckets = append(buckets, ActivityBucket{Start: bucketStart})
	}
	for _, row := range rows {
		bucketStart, err := time.Parse("2006-01-02", row.BucketDate)
		if err != nil {
			return nil, fmt.Errorf("parse deployment activity bucket %q: %w", row.BucketDate, err)
		}
		index, found := byStart[bucketStart]
		if !found {
			return nil, fmt.Errorf("deployment activity bucket %s outside requested range", bucketStart)
		}
		bucket := &buckets[index]
		switch row.Status {
		case deploymentmodel.Succeeded:
			bucket.Succeeded = row.Count
		case deploymentmodel.Failed:
			bucket.Failed = row.Count
		case deploymentmodel.Running:
			bucket.Running = row.Count
		case deploymentmodel.Interrupted:
			bucket.Interrupted = row.Count
		case deploymentmodel.Superseded:
			bucket.Superseded = row.Count
		default:
			return nil, fmt.Errorf("unsupported deployment activity status %q", row.Status)
		}
	}
	return buckets, nil
}

func nextActivityBucket(start time.Time, interval ActivityInterval) time.Time {
	if interval == ActivityIntervalWeekly {
		return start.AddDate(0, 0, activityDaysPerWeek)
	}
	return start.AddDate(0, 0, 1)
}

var summaryColumns = []any{
	"id", "stack", "commit", "status", "error_code", "phase", "started_at", "finished_at", "details_payload",
	goqu.L(`(SELECT attempt.id FROM deployment_attempts AS attempt
		WHERE attempt.deployment_id = deployments.id ORDER BY attempt.number DESC LIMIT 1)`).As("latest_attempt_id"),
	goqu.L(`(SELECT count(*) FROM deployment_attempts AS attempt
		WHERE attempt.deployment_id = deployments.id)`).As("attempt_count"),
	goqu.L(`CASE WHEN deployments.status IN ('failed','interrupted') AND EXISTS (
		SELECT 1 FROM deployments AS newer
		WHERE newer.stack = deployments.stack AND newer.rowid > deployments.rowid
	) THEN 1 ELSE 0 END`).As("is_superseded"),
}

const supersededPredicate = `deployments.status IN ('failed','interrupted') AND EXISTS (
	SELECT 1 FROM deployments AS newer
	WHERE newer.stack = deployments.stack AND newer.rowid > deployments.rowid
)`

// List returns a lightweight page without loading desired snapshots or change arrays.
func (s *Store) List(ctx context.Context, filter deploymentmodel.ListFilter) (deploymentmodel.Page, error) {
	dataset := s.db.Dialect().From("deployments").Prepared(true).Select(summaryColumns...)
	dataset = filterDeploymentIdentities(dataset, filter)
	if filter.Status != "" {
		if filter.Status == deploymentmodel.Superseded {
			dataset = dataset.Where(goqu.L(supersededPredicate))
		} else {
			dataset = dataset.Where(
				goqu.Ex{"status": filter.Status},
				goqu.L("NOT ("+supersededPredicate+")"),
			)
		}
	}
	if filter.ErrorCode != "" {
		dataset = dataset.Where(goqu.Ex{"error_code": filter.ErrorCode})
	}
	if filter.Phase != "" {
		dataset = dataset.Where(goqu.Ex{"phase": filter.Phase})
	}
	if filter.Cursor != "" {
		started, id, err := decodeCursor(filter.Cursor)
		if err != nil {
			return deploymentmodel.Page{}, err
		}
		startedAt := time.Unix(0, started).UTC()
		dataset = dataset.Where(goqu.Or(
			goqu.C("started_at").Lt(startedAt),
			goqu.And(goqu.C("started_at").Eq(startedAt), goqu.C("id").Gt(id)),
		))
	}
	limit := filter.Limit
	if limit < 1 || limit > 100 {
		limit = 20
	}
	dataset = dataset.Order(goqu.I("started_at").Desc(), goqu.I("id").Asc()).Limit(uint(limit + 1))
	query, args, err := dataset.ToSQL()
	if err != nil {
		return deploymentmodel.Page{}, err
	}
	var rows []summaryRow
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return deploymentmodel.Page{}, err
	}
	items := make([]deploymentmodel.DeploymentSummary, 0, len(rows))
	for _, row := range rows {
		item, convertErr := row.toSummary()
		if convertErr != nil {
			return deploymentmodel.Page{}, convertErr
		}
		items = append(items, item)
	}
	page := deploymentmodel.Page{Deployments: items}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = encodeCursor(last.StartedAt.UnixNano(), last.ID)
		page.Deployments = items[:limit]
	}
	return page, nil
}

func filterDeploymentIdentities(
	dataset *goqu.SelectDataset,
	filter deploymentmodel.ListFilter,
) *goqu.SelectDataset {
	if len(filter.Stacks) > 0 {
		dataset = dataset.Where(goqu.Ex{"stack": filter.Stacks})
	}
	if len(filter.Services) > 0 {
		serviceNames := goqu.L("json_extract(svc.value, '$.name')").In(filter.Services)
		dataset = dataset.Where(goqu.L(`EXISTS (SELECT 1 FROM json_each(deployments.desired_payload,
			'$.definition.compose.services') AS svc WHERE ?)`, serviceNames))
	}
	return dataset
}

// ListServiceDeployments returns only recent attempts and display fields for one service.
func (s *Store) ListServiceDeployments(
	ctx context.Context,
	stack string,
	service string,
	limit int,
) ([]deploymentmodel.ServiceDeployment, error) {
	if limit < 1 || limit > 100 {
		limit = 5
	}
	const query = `
		SELECT d.id, d."commit", d.status,
			CASE WHEN d.status IN ('failed','interrupted') AND EXISTS (
				SELECT 1 FROM deployments AS newer
				WHERE newer.stack = d.stack AND newer.rowid > d.rowid
			) THEN 1 ELSE 0 END AS is_superseded,
			d.started_at,
			json_extract(svc.value, '$.image') AS image
		FROM deployments AS d,
			json_each(d.desired_payload, '$.definition.compose.services') AS svc
		WHERE d.stack = ? AND json_extract(svc.value, '$.name') = ?
		ORDER BY d.started_at DESC, d.id ASC
		LIMIT ?`
	var rows []struct {
		ID           string                 `db:"id"`
		Commit       string                 `db:"commit"`
		Status       deploymentmodel.Status `db:"status"`
		IsSuperseded bool                   `db:"is_superseded"`
		StartedAt    time.Time              `db:"started_at"`
		Image        string                 `db:"image"`
	}
	if err := s.db.Select(ctx, &rows, query, stack, service, limit); err != nil {
		return nil, err
	}
	items := make([]deploymentmodel.ServiceDeployment, 0, len(rows))
	for _, row := range rows {
		items = append(items, deploymentmodel.ServiceDeployment{
			ID: row.ID, Commit: row.Commit, Status: projectedStatus(row.Status, row.IsSuperseded),
			StartedAt: row.StartedAt.UTC(), Image: row.Image,
		})
	}
	return items, nil
}

type summaryRow struct {
	ID              string                 `db:"id"`
	Stack           string                 `db:"stack"`
	Commit          string                 `db:"commit"`
	Status          deploymentmodel.Status `db:"status"`
	ErrorCode       string                 `db:"error_code"`
	Phase           deploymentmodel.Phase  `db:"phase"`
	StartedAt       time.Time              `db:"started_at"`
	FinishedAt      sql.NullTime           `db:"finished_at"`
	DetailsPayload  []byte                 `db:"details_payload"`
	LatestAttemptID sql.NullString         `db:"latest_attempt_id"`
	AttemptCount    int                    `db:"attempt_count"`
	IsSuperseded    bool                   `db:"is_superseded"`
}

type deploymentDetails struct {
	// UnresolvedServices retains verification obligations across desired-state deployments.
	UnresolvedServices []string                          `json:"unresolved_services"`
	ErrorDiagnostic    string                            `json:"error_diagnostic,omitempty"`
	Retryable          bool                              `json:"retryable,omitempty"`
	ApplyStatus        deploymentmodel.StageStatus       `json:"apply_status"`
	VerificationStatus deploymentmodel.StageStatus       `json:"verification_status"`
	CleanupStatus      deploymentmodel.StageStatus       `json:"cleanup_status"`
	ActualStateStatus  deploymentmodel.ActualStateStatus `json:"actual_state_status"`
	ObservedAt         *time.Time                        `json:"observed_at,omitempty"`
	ComparisonBasis    deploymentmodel.ComparisonBasis   `json:"comparison_basis"`
	ComparisonStatus   deploymentmodel.ComparisonStatus  `json:"comparison_status"`
	BasisDeploymentID  string                            `json:"basis_deployment_id,omitempty"`
	Summary            deploymentmodel.ChangeSummary     `json:"summary"`
	Resources          deploymentmodel.ResourceSummary   `json:"resources"`
	DiffAvailable      bool                              `json:"diff_available"`
}

type deploymentRow struct {
	ID              string                 `db:"id"`
	Stack           string                 `db:"stack"`
	Commit          string                 `db:"commit"`
	Status          deploymentmodel.Status `db:"status"`
	ErrorCode       string                 `db:"error_code"`
	Phase           deploymentmodel.Phase  `db:"phase"`
	StartedAt       time.Time              `db:"started_at"`
	FinishedAt      sql.NullTime           `db:"finished_at"`
	DetailsPayload  []byte                 `db:"details_payload"`
	ChangesPayload  []byte                 `db:"changes_payload"`
	LatestAttemptID sql.NullString         `db:"latest_attempt_id"`
	AttemptCount    int                    `db:"attempt_count"`
	IsSuperseded    bool                   `db:"is_superseded"`
}

type attemptRow struct {
	ID             string                 `db:"id"`
	DeploymentID   string                 `db:"deployment_id"`
	Number         int                    `db:"number"`
	Status         deploymentmodel.Status `db:"status"`
	ErrorCode      string                 `db:"error_code"`
	Phase          deploymentmodel.Phase  `db:"phase"`
	StartedAt      time.Time              `db:"started_at"`
	FinishedAt     sql.NullTime           `db:"finished_at"`
	DetailsPayload []byte                 `db:"details_payload"`
}

func detailsFromDeployment(d deploymentmodel.Deployment) deploymentDetails {
	return deploymentDetails{
		UnresolvedServices: persistedUnresolvedServices(d.Verification),
		ErrorDiagnostic:    d.ErrorDiagnostic, Retryable: d.Retryable, ApplyStatus: d.ApplyStatus,
		VerificationStatus: d.VerificationStatus, CleanupStatus: d.CleanupStatus,
		ActualStateStatus: d.ActualStateStatus, ObservedAt: d.ObservedAt,
		ComparisonBasis: d.ComparisonBasis, ComparisonStatus: d.ComparisonStatus,
		BasisDeploymentID: d.BasisDeploymentID, Summary: d.Summary,
		Resources: d.Resources, DiffAvailable: d.DiffAvailable,
	}
}

func marshalDeploymentPayloads(d deploymentmodel.Deployment) (string, string, error) {
	details, err := json.Marshal(detailsFromDeployment(d))
	if err != nil {
		return "", "", err
	}
	changes := d.Changes
	if changes == nil {
		changes = []deploymentmodel.Change{}
	}
	changesPayload, err := json.Marshal(changes)
	if err != nil {
		return "", "", err
	}
	return string(details), string(changesPayload), nil
}

func projectedStatus(status deploymentmodel.Status, isSuperseded bool) deploymentmodel.Status {
	if isSuperseded {
		return deploymentmodel.Superseded
	}
	return status
}

func (row deploymentRow) deployment() (deploymentmodel.Deployment, error) {
	var details deploymentDetails
	if err := json.Unmarshal(row.DetailsPayload, &details); err != nil {
		return deploymentmodel.Deployment{}, err
	}
	var changes []deploymentmodel.Change
	if err := json.Unmarshal(row.ChangesPayload, &changes); err != nil {
		return deploymentmodel.Deployment{}, err
	}
	result := deploymentmodel.Deployment{
		ID: row.ID, Stack: row.Stack, Commit: row.Commit, Status: projectedStatus(row.Status, row.IsSuperseded),
		Verification: deploymentmodel.Verification{
			Known: details.UnresolvedServices != nil, UnresolvedServices: details.UnresolvedServices,
		},
		StartedAt: row.StartedAt.UTC(), ErrorCode: row.ErrorCode,
		ErrorDiagnostic: details.ErrorDiagnostic, Retryable: details.Retryable, Phase: row.Phase,
		ApplyStatus: details.ApplyStatus, VerificationStatus: details.VerificationStatus,
		CleanupStatus: details.CleanupStatus, ActualStateStatus: details.ActualStateStatus,
		ObservedAt: details.ObservedAt, ComparisonBasis: details.ComparisonBasis,
		ComparisonStatus: details.ComparisonStatus, BasisDeploymentID: details.BasisDeploymentID,
		Summary: details.Summary, Resources: details.Resources, Changes: changes,
		DiffAvailable: details.DiffAvailable, LatestAttemptID: row.LatestAttemptID.String,
		AttemptCount: row.AttemptCount,
	}
	if row.FinishedAt.Valid {
		finished := row.FinishedAt.Time.UTC()
		result.FinishedAt = &finished
	}
	return result, nil
}

func (row summaryRow) toSummary() (deploymentmodel.DeploymentSummary, error) {
	var details deploymentDetails
	if err := json.Unmarshal(row.DetailsPayload, &details); err != nil {
		return deploymentmodel.DeploymentSummary{}, err
	}
	item := deploymentmodel.DeploymentSummary{
		ID:                 row.ID,
		Stack:              row.Stack,
		Commit:             row.Commit,
		Status:             projectedStatus(row.Status, row.IsSuperseded),
		StartedAt:          row.StartedAt.UTC(),
		ErrorCode:          row.ErrorCode,
		Phase:              row.Phase,
		ApplyStatus:        details.ApplyStatus,
		VerificationStatus: details.VerificationStatus,
		CleanupStatus:      details.CleanupStatus,
		ActualStateStatus:  details.ActualStateStatus,
		ObservedAt:         details.ObservedAt,
		ComparisonBasis:    details.ComparisonBasis,
		ComparisonStatus:   details.ComparisonStatus,
		BasisDeploymentID:  details.BasisDeploymentID,
		Summary:            details.Summary,
		Resources:          details.Resources,
		DiffAvailable:      details.DiffAvailable,
		LatestAttemptID:    row.LatestAttemptID.String,
		AttemptCount:       row.AttemptCount,
	}
	if row.FinishedAt.Valid {
		value := row.FinishedAt.Time.UTC()
		item.FinishedAt = &value
	}
	return item, nil
}

func (row attemptRow) attempt() (deploymentmodel.Attempt, error) {
	var details deploymentDetails
	if err := json.Unmarshal(row.DetailsPayload, &details); err != nil {
		return deploymentmodel.Attempt{}, err
	}
	result := deploymentmodel.Attempt{
		ID: row.ID, DeploymentID: row.DeploymentID, Number: row.Number, Status: row.Status,
		StartedAt: row.StartedAt.UTC(), ErrorCode: row.ErrorCode,
		ErrorDiagnostic: details.ErrorDiagnostic, Retryable: details.Retryable, Phase: row.Phase,
		ApplyStatus: details.ApplyStatus, VerificationStatus: details.VerificationStatus,
		CleanupStatus: details.CleanupStatus, ActualStateStatus: details.ActualStateStatus,
		ObservedAt: details.ObservedAt,
	}
	if row.FinishedAt.Valid {
		finished := row.FinishedAt.Time.UTC()
		result.FinishedAt = &finished
	}
	return result, nil
}

type initJobExecutionRow struct {
	AttemptID     string                        `db:"attempt_id"`
	Ordinal       int                           `db:"ordinal"`
	ServiceName   string                        `db:"service_name"`
	JobName       string                        `db:"job_name"`
	Status        deploymentmodel.InitJobStatus `db:"status"`
	StartedAt     time.Time                     `db:"started_at"`
	FinishedAt    time.Time                     `db:"finished_at"`
	LogsAvailable bool                          `db:"logs_available"`
	LogsTruncated bool                          `db:"logs_truncated"`
}

func (s *Store) initJobs(
	ctx context.Context,
	deploymentID string,
) (map[string][]deploymentmodel.InitJobExecution, error) {
	const query = `SELECT job.attempt_id,job.ordinal,job.service_name,job.job_name,job.status,
		job.started_at,job.finished_at,job.logs IS NOT NULL AS logs_available,job.logs_truncated
		FROM deployment_init_jobs AS job
		JOIN deployment_attempts AS attempt ON attempt.id = job.attempt_id
		WHERE attempt.deployment_id = ?
		ORDER BY attempt.number DESC,job.ordinal ASC`
	var rows []initJobExecutionRow
	if err := s.db.Select(ctx, &rows, query, deploymentID); err != nil {
		return nil, err
	}
	result := make(map[string][]deploymentmodel.InitJobExecution)
	for _, row := range rows {
		result[row.AttemptID] = append(result[row.AttemptID], deploymentmodel.InitJobExecution{
			AttemptID: row.AttemptID, Ordinal: row.Ordinal, ServiceName: row.ServiceName, JobName: row.JobName,
			Status: row.Status, StartedAt: row.StartedAt.UTC(), FinishedAt: row.FinishedAt.UTC(),
			LogsAvailable: row.LogsAvailable, LogsTruncated: row.LogsTruncated,
		})
	}
	return result, nil
}

func (s *Store) attempts(ctx context.Context, deploymentID string) ([]deploymentmodel.Attempt, error) {
	query, args, err := s.db.Dialect().From("deployment_attempts").Prepared(true).
		Select("id", "deployment_id", "number", "status", "error_code", "phase",
			"started_at", "finished_at", "details_payload").
		Where(goqu.Ex{"deployment_id": deploymentID}).Order(goqu.I("number").Desc()).ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []attemptRow
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	result := make([]deploymentmodel.Attempt, 0, len(rows))
	for _, row := range rows {
		attempt, convertErr := row.attempt()
		if convertErr != nil {
			return nil, convertErr
		}
		result = append(result, attempt)
	}
	return result, nil
}

// GetAttempt loads one persisted deployment attempt.
func (s *Store) GetAttempt(ctx context.Context, id string) (deploymentmodel.Attempt, error) {
	query, args, err := s.db.Dialect().From("deployment_attempts").Prepared(true).
		Select("id", "deployment_id", "number", "status", "error_code", "phase",
			"started_at", "finished_at", "details_payload").
		Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return deploymentmodel.Attempt{}, err
	}
	var row attemptRow
	if err = s.db.Get(ctx, &row, query, args...); err != nil {
		return deploymentmodel.Attempt{}, notFound(err)
	}
	return row.attempt()
}

// RunningAttempts returns persisted running attempts, optionally restricted to one stack.
func (s *Store) RunningAttempts(ctx context.Context, stack string) ([]deploymentmodel.Attempt, error) {
	dataset := s.db.Dialect().From(goqu.T("deployment_attempts").As("attempt")).Prepared(true).
		Join(goqu.T("deployments").As("deployment"),
			goqu.On(goqu.I("deployment.id").Eq(goqu.I("attempt.deployment_id")))).
		Select(
			goqu.I("attempt.id"), goqu.I("attempt.deployment_id"), goqu.I("attempt.number"),
			goqu.I("attempt.status"), goqu.I("attempt.error_code"), goqu.I("attempt.phase"),
			goqu.I("attempt.started_at"), goqu.I("attempt.finished_at"), goqu.I("attempt.details_payload"),
		).Where(goqu.Ex{"attempt.status": deploymentmodel.Running})
	if stack != "" {
		dataset = dataset.Where(goqu.Ex{"deployment.stack": stack})
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []attemptRow
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	result := make([]deploymentmodel.Attempt, 0, len(rows))
	for _, row := range rows {
		attempt, convertErr := row.attempt()
		if convertErr != nil {
			return nil, convertErr
		}
		result = append(result, attempt)
	}
	return result, nil
}

func encodeCursor(started int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(started, 10) + ":" + id))
}

func decodeCursor(cursor string) (int64, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", ErrInvalidCursor
	}
	startedRaw, id, ok := strings.Cut(string(decoded), ":")
	if !ok || id == "" {
		return 0, "", ErrInvalidCursor
	}
	started, err := strconv.ParseInt(startedRaw, 10, 64)
	if err != nil {
		return 0, "", ErrInvalidCursor
	}
	return started, id, nil
}

// Get loads one desired-state deployment with all attempts in newest-first order.
func (s *Store) Get(ctx context.Context, id string) (deploymentmodel.Deployment, error) {
	var row deploymentRow
	columns := append([]any{}, summaryColumns...)
	columns = append(columns, "changes_payload")
	query, args, err := s.db.Dialect().From("deployments").Prepared(true).
		Select(columns...).
		Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return deploymentmodel.Deployment{}, err
	}
	if err = s.db.Get(ctx, &row, query, args...); err != nil {
		return deploymentmodel.Deployment{}, notFound(err)
	}
	result, err := row.deployment()
	if err != nil {
		return deploymentmodel.Deployment{}, err
	}
	result.Attempts, err = s.attempts(ctx, id)
	if err != nil {
		return deploymentmodel.Deployment{}, err
	}
	initJobs, err := s.initJobs(ctx, id)
	if err != nil {
		return deploymentmodel.Deployment{}, err
	}
	for i := range result.Attempts {
		result.Attempts[i].InitJobs = initJobs[result.Attempts[i].ID]
	}
	return result, nil
}

// SaveInitJobExecution persists one completed Init Job execution independently from Attempt completion.
func (s *Store) SaveInitJobExecution(ctx context.Context, execution deploymentmodel.InitJobExecution) error {
	if execution.StartedAt.IsZero() || execution.FinishedAt.IsZero() {
		return fmt.Errorf("init job execution timestamps are required")
	}
	var logs any
	if execution.LogsAvailable {
		logs = execution.Logs
	}
	query, args, err := s.db.Dialect().Insert("deployment_init_jobs").Prepared(true).Rows(goqu.Record{
		"attempt_id": execution.AttemptID, "ordinal": execution.Ordinal,
		"service_name": execution.ServiceName, "job_name": execution.JobName, "status": execution.Status,
		"started_at": execution.StartedAt.UTC(), "finished_at": execution.FinishedAt.UTC(),
		"logs": logs, "logs_truncated": execution.LogsTruncated,
	}).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// InitJobLogs returns exact stored bytes after validating Deployment, Attempt and execution relationships.
func (s *Store) InitJobLogs(
	ctx context.Context,
	deploymentID string,
	attemptID string,
	ordinal int,
) ([]byte, error) {
	const query = `SELECT job.logs IS NOT NULL AS logs_available,job.logs
		FROM deployment_init_jobs AS job
		JOIN deployment_attempts AS attempt ON attempt.id = job.attempt_id
		WHERE attempt.deployment_id = ? AND job.attempt_id = ? AND job.ordinal = ?`
	var row struct {
		LogsAvailable bool   `db:"logs_available"`
		Logs          []byte `db:"logs"`
	}
	if err := s.db.Get(ctx, &row, query, deploymentID, attemptID, ordinal); err != nil {
		return nil, notFound(err)
	}
	if !row.LogsAvailable {
		return nil, ErrLogsUnavailable
	}
	if row.Logs == nil {
		return []byte{}, nil
	}
	return row.Logs, nil
}

// Desired loads a deployment's safe desired definition for independent subscribers.
func (s *Store) Desired(ctx context.Context, id string) (compose.File, error) {
	query, args, err := s.db.Dialect().From("deployments").Prepared(true).
		Select("desired_payload").Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return compose.File{}, err
	}
	desired, err := s.getPrepared(ctx, query, args...)
	return desired.Definition, notFound(err)
}

// Baseline returns only the last successfully committed effective desired state.
func (s *Store) Baseline(ctx context.Context, stack string) (deploymentmodel.Prepared, error) {
	query, args, err := s.db.Dialect().From(goqu.T("desired_snapshots").As("snapshot")).Prepared(true).
		Join(goqu.T("deployments").As("deployment"),
			goqu.On(goqu.I("deployment.id").Eq(goqu.I("snapshot.deployment_id")))).
		Select(goqu.I("deployment.desired_payload")).
		Where(goqu.Ex{"snapshot.stack": stack, "deployment.status": deploymentmodel.Succeeded}).ToSQL()
	if err != nil {
		return deploymentmodel.Prepared{}, err
	}
	desired, err := s.getPrepared(ctx, query, args...)
	return desired, notFound(err)
}

func (s *Store) getPrepared(ctx context.Context, query string, args ...any) (deploymentmodel.Prepared, error) {
	var payload []byte
	if err := s.db.Get(ctx, &payload, query, args...); err != nil {
		return deploymentmodel.Prepared{}, err
	}
	var desired deploymentmodel.Prepared
	err := json.Unmarshal(payload, &desired)
	return desired, err
}

// BaselineDeploymentID returns the deployment backing a stack's successful baseline.
func (s *Store) BaselineDeploymentID(ctx context.Context, stack string) (string, error) {
	var id string
	query, args, err := s.db.Dialect().From("desired_snapshots").Prepared(true).
		Select("deployment_id").Where(goqu.Ex{"stack": stack}).ToSQL()
	if err != nil {
		return "", err
	}
	err = s.db.Get(ctx, &id, query, args...)
	return id, notFound(err)
}

// Latest returns a stack's newest deployment and prepared desired state.
func (s *Store) Latest(
	ctx context.Context,
	stack string,
) (deploymentmodel.Deployment, deploymentmodel.Prepared, error) {
	var id string
	query, args, err := s.db.Dialect().From("deployments").Prepared(true).Select("id").
		Where(goqu.Ex{"stack": stack}).Order(goqu.I("rowid").Desc()).Limit(1).ToSQL()
	if err != nil {
		return deploymentmodel.Deployment{}, deploymentmodel.Prepared{}, err
	}
	if err = s.db.Get(ctx, &id, query, args...); err != nil {
		return deploymentmodel.Deployment{}, deploymentmodel.Prepared{}, notFound(err)
	}
	deployment, err := s.Get(ctx, id)
	if err != nil {
		return deploymentmodel.Deployment{}, deploymentmodel.Prepared{}, err
	}
	prepared, err := s.prepared(ctx, id)
	return deployment, prepared, err
}

// LegacyRunning returns running deployments that predate persisted attempts.
func (s *Store) LegacyRunning(ctx context.Context, stack string) ([]deploymentmodel.Deployment, error) {
	dataset := s.db.Dialect().From("deployments").Prepared(true).Select("id").
		Where(
			goqu.Ex{"status": deploymentmodel.Running},
			goqu.L(`NOT EXISTS (SELECT 1 FROM deployment_attempts AS attempt
				WHERE attempt.deployment_id = deployments.id)`),
		)
	if stack != "" {
		dataset = dataset.Where(goqu.Ex{"stack": stack})
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, err
	}
	var ids []string
	if err = s.db.Select(ctx, &ids, query, args...); err != nil {
		return nil, err
	}
	result := make([]deploymentmodel.Deployment, 0, len(ids))
	for _, id := range ids {
		deployment, getErr := s.Get(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		result = append(result, deployment)
	}
	return result, nil
}

func (s *Store) prepared(ctx context.Context, id string) (deploymentmodel.Prepared, error) {
	query, args, err := s.db.Dialect().From("deployments").Prepared(true).
		Select("desired_payload").Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return deploymentmodel.Prepared{}, err
	}
	prepared, err := s.getPrepared(ctx, query, args...)
	return prepared, notFound(err)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// CreateDeployment persists a new deployment and its first attempt.
func (s *Store) CreateDeployment(
	ctx context.Context,
	d deploymentmodel.Deployment,
	desired deploymentmodel.Prepared,
	attempt deploymentmodel.Attempt,
) error {
	if d.StartedAt.IsZero() {
		return fmt.Errorf("deployment start time is required")
	}
	safe, err := json.Marshal(desired)
	if err != nil {
		return err
	}
	details, changes, err := marshalDeploymentPayloads(d)
	if err != nil {
		return err
	}
	query, args, err := s.db.Dialect().Insert("deployments").Prepared(true).Rows(goqu.Record{
		"id": d.ID, "stack": d.Stack, "commit": d.Commit, "status": d.Status,
		"error_code": d.ErrorCode, "phase": d.Phase, "started_at": d.StartedAt.UTC(),
		"desired_payload": string(safe), "details_payload": details, "changes_payload": changes,
	}).ToSQL()
	if err != nil {
		return err
	}
	if _, err = s.db.Exec(ctx, query, args...); err != nil {
		return err
	}
	return s.insertAttemptRow(ctx, attempt)
}

// StartAttempt persists a retry and updates the deployment projection to running.
func (s *Store) StartAttempt(ctx context.Context, d deploymentmodel.Deployment, attempt deploymentmodel.Attempt) error {
	if err := s.insertAttemptRow(ctx, attempt); err != nil {
		return err
	}
	d.ErrorDiagnostic, d.ApplyStatus = "", deploymentmodel.StageRunning
	d.Retryable = false
	d.VerificationStatus, d.CleanupStatus = deploymentmodel.StagePending, deploymentmodel.StagePending
	d.ActualStateStatus, d.ObservedAt = deploymentmodel.ActualStateUnknown, nil
	details, _, err := marshalDeploymentPayloads(d)
	if err != nil {
		return err
	}
	query, args, err := s.db.Dialect().Update("deployments").Prepared(true).Set(goqu.Record{
		"status": deploymentmodel.Running, "error_code": "", "phase": deploymentmodel.PhaseApply,
		"finished_at": nil, "details_payload": details,
	}).Where(
		goqu.Ex{"id": d.ID, "status": []deploymentmodel.Status{deploymentmodel.Failed, deploymentmodel.Interrupted}},
	).ToSQL()
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	return requireOneRunning(result)
}

// InsertLegacyAttempt materializes an attempt for a deployment created before attempt persistence.
func (s *Store) InsertLegacyAttempt(ctx context.Context, attempt deploymentmodel.Attempt) error {
	return s.insertAttemptRow(ctx, attempt)
}

func (s *Store) insertAttemptRow(ctx context.Context, attempt deploymentmodel.Attempt) error {
	if attempt.StartedAt.IsZero() {
		return fmt.Errorf("attempt start time is required")
	}
	details, err := json.Marshal(deploymentDetails{
		ApplyStatus: attempt.ApplyStatus, VerificationStatus: attempt.VerificationStatus,
		CleanupStatus: attempt.CleanupStatus, ActualStateStatus: attempt.ActualStateStatus,
	})
	if err != nil {
		return err
	}
	query, args, err := s.db.Dialect().Insert("deployment_attempts").Prepared(true).Rows(goqu.Record{
		"id": attempt.ID, "deployment_id": attempt.DeploymentID, "number": attempt.Number,
		"status": attempt.Status, "error_code": attempt.ErrorCode, "phase": attempt.Phase,
		"started_at": attempt.StartedAt.UTC(), "details_payload": string(details),
	}).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// FinishAttempt persists a terminal attempt and its deployment projection.
func (s *Store) FinishAttempt(
	ctx context.Context,
	d deploymentmodel.Deployment,
	attempt deploymentmodel.Attempt,
) error {
	if attempt.FinishedAt == nil || attempt.FinishedAt.IsZero() {
		return fmt.Errorf("terminal attempt requires finish time")
	}
	attemptDetails, err := json.Marshal(deploymentDetails{
		ErrorDiagnostic: attempt.ErrorDiagnostic, Retryable: attempt.Retryable, ApplyStatus: attempt.ApplyStatus,
		VerificationStatus: attempt.VerificationStatus, CleanupStatus: attempt.CleanupStatus,
		ActualStateStatus: attempt.ActualStateStatus, ObservedAt: attempt.ObservedAt,
	})
	if err != nil {
		return err
	}
	query, args, err := s.db.Dialect().Update("deployment_attempts").Prepared(true).Set(goqu.Record{
		"status": attempt.Status, "error_code": attempt.ErrorCode, "phase": attempt.Phase,
		"finished_at": attempt.FinishedAt.UTC(), "details_payload": string(attemptDetails),
	}).Where(goqu.Ex{"id": attempt.ID, "deployment_id": d.ID, "status": deploymentmodel.Running}).ToSQL()
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if err = requireOneRunning(result); err != nil {
		return err
	}
	d.ErrorDiagnostic, d.ApplyStatus = attempt.ErrorDiagnostic, attempt.ApplyStatus
	d.Retryable = attempt.Retryable
	d.VerificationStatus, d.CleanupStatus = attempt.VerificationStatus, attempt.CleanupStatus
	d.ActualStateStatus, d.ObservedAt = attempt.ActualStateStatus, attempt.ObservedAt
	details, _, err := marshalDeploymentPayloads(d)
	if err != nil {
		return err
	}
	query, args, err = s.db.Dialect().Update("deployments").Prepared(true).Set(goqu.Record{
		"status": attempt.Status, "error_code": attempt.ErrorCode, "phase": attempt.Phase,
		"finished_at": attempt.FinishedAt.UTC(), "details_payload": details,
	}).Where(
		goqu.Ex{"id": d.ID, "status": deploymentmodel.Running},
		goqu.L(`NOT EXISTS (SELECT 1 FROM deployment_attempts AS newer
			WHERE newer.deployment_id = deployments.id AND newer.number > ?)`, attempt.Number),
	).ToSQL()
	if err != nil {
		return err
	}
	result, err = s.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	return requireOneRunning(result)
}

// SynchronizeRunningAttempt repairs an attempt from an already-terminal deployment projection.
func (s *Store) SynchronizeRunningAttempt(
	ctx context.Context,
	attempt deploymentmodel.Attempt,
) (bool, error) {
	const query = `UPDATE deployment_attempts
		SET status = (SELECT status FROM deployments WHERE id = deployment_attempts.deployment_id),
			error_code = (SELECT error_code FROM deployments WHERE id = deployment_attempts.deployment_id),
			phase = (SELECT phase FROM deployments WHERE id = deployment_attempts.deployment_id),
			finished_at = (SELECT finished_at FROM deployments WHERE id = deployment_attempts.deployment_id),
			details_payload = (SELECT details_payload FROM deployments WHERE id = deployment_attempts.deployment_id)
		WHERE id = ? AND deployment_id = ? AND status = 'running'
			AND EXISTS (
				SELECT 1 FROM deployments
				WHERE id = deployment_attempts.deployment_id
					AND status IN ('succeeded', 'failed', 'interrupted')
			)`
	result, err := s.db.Exec(ctx, query, attempt.ID, attempt.DeploymentID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

func requireOneRunning(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotRunning
	}
	return nil
}

// SaveBaseline records a successful deployment as the stack's current baseline.
func (s *Store) SaveBaseline(ctx context.Context, id string) error {
	source := s.db.Dialect().From("deployments").Prepared(true).
		Select("stack", goqu.I("id").As("deployment_id")).
		Where(goqu.Ex{"id": id, "status": "succeeded"})
	query, args, err := s.db.Dialect().Insert("desired_snapshots").Prepared(true).
		Cols("stack", "deployment_id").
		FromQuery(source).
		OnConflict(goqu.DoUpdate("stack", goqu.Record{
			"deployment_id": goqu.I("excluded.deployment_id"),
		})).
		ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// Keep the historical null/array wire format solely at the persistence boundary.
func persistedUnresolvedServices(verification deploymentmodel.Verification) []string {
	if !verification.Known {
		return nil
	}
	return append([]string{}, verification.UnresolvedServices...)
}
