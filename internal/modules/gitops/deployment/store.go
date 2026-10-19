package deployment

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
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// ErrNotFound means the requested deployment, attempt or successful baseline does not exist.
var ErrNotFound = errors.New("deployment not found")

// ErrNotRunning prevents overwriting a terminal deployment outcome.
var ErrNotRunning = errors.New("deployment is not running")

// ErrInconsistentDeployment means controller state disagrees with the persisted deployment.
var ErrInconsistentDeployment = errors.New("deployment row and payload are inconsistent")

// ErrInvalidCursor means a list cursor is malformed or unsupported.
var ErrInvalidCursor = errors.New("invalid deployment cursor")

var dialect = goqu.Dialect("sqlite3")

// Store provides context-aware deployment and snapshot queries.
type Store struct{ db *storage.Database }

// NewStore creates a deployment repository on the application database.
func NewStore(db *storage.Database) *Store { return &Store{db: db} }

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
func (s *Store) List(ctx context.Context, filter ListFilter) (Page, error) {
	dataset := dialect.From("deployments").Prepared(true).Select(summaryColumns...)
	if filter.Stack != "" {
		dataset = dataset.Where(goqu.Ex{"stack": filter.Stack})
	}
	if filter.Service != "" {
		const serviceQuery = `EXISTS (SELECT 1 FROM json_each(deployments.desired_payload,
			'$.definition.compose.services') AS svc WHERE json_extract(svc.value, '$.name') = ?)`
		dataset = dataset.Where(goqu.L(serviceQuery, filter.Service))
	}
	if filter.Status != "" {
		if filter.Status == Superseded {
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
			return Page{}, err
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
		return Page{}, err
	}
	var rows []summaryRow
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return Page{}, err
	}
	items := make([]DeploymentSummary, 0, len(rows))
	for _, row := range rows {
		item, convertErr := row.toSummary()
		if convertErr != nil {
			return Page{}, convertErr
		}
		items = append(items, item)
	}
	page := Page{Deployments: items}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = encodeCursor(last.StartedAt.UnixNano(), last.ID)
		page.Deployments = items[:limit]
	}
	return page, nil
}

// ListServiceDeployments returns only recent attempts and display fields for one service.
func (s *Store) ListServiceDeployments(
	ctx context.Context,
	stack string,
	service string,
	limit int,
) ([]ServiceDeployment, error) {
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
		ID           string    `db:"id"`
		Commit       string    `db:"commit"`
		Status       Status    `db:"status"`
		IsSuperseded bool      `db:"is_superseded"`
		StartedAt    time.Time `db:"started_at"`
		Image        string    `db:"image"`
	}
	if err := s.db.Select(ctx, &rows, query, stack, service, limit); err != nil {
		return nil, err
	}
	items := make([]ServiceDeployment, 0, len(rows))
	for _, row := range rows {
		items = append(items, ServiceDeployment{
			ID: row.ID, Commit: row.Commit, Status: projectedStatus(row.Status, row.IsSuperseded),
			StartedAt: row.StartedAt.UTC(), Image: row.Image,
		})
	}
	return items, nil
}

type summaryRow struct {
	ID              string         `db:"id"`
	Stack           string         `db:"stack"`
	Commit          string         `db:"commit"`
	Status          Status         `db:"status"`
	ErrorCode       string         `db:"error_code"`
	Phase           Phase          `db:"phase"`
	StartedAt       time.Time      `db:"started_at"`
	FinishedAt      sql.NullTime   `db:"finished_at"`
	DetailsPayload  []byte         `db:"details_payload"`
	LatestAttemptID sql.NullString `db:"latest_attempt_id"`
	AttemptCount    int            `db:"attempt_count"`
	IsSuperseded    bool           `db:"is_superseded"`
}

type deploymentDetails struct {
	ErrorDiagnostic    string            `json:"error_diagnostic,omitempty"`
	ApplyStatus        StageStatus       `json:"apply_status"`
	VerificationStatus StageStatus       `json:"verification_status"`
	CleanupStatus      StageStatus       `json:"cleanup_status"`
	ActualStateStatus  ActualStateStatus `json:"actual_state_status"`
	ObservedAt         *time.Time        `json:"observed_at,omitempty"`
	ComparisonBasis    ComparisonBasis   `json:"comparison_basis"`
	ComparisonStatus   ComparisonStatus  `json:"comparison_status"`
	BasisDeploymentID  string            `json:"basis_deployment_id,omitempty"`
	Summary            ChangeSummary     `json:"summary"`
	Resources          ResourceSummary   `json:"resources"`
	DiffAvailable      bool              `json:"diff_available"`
}

type deploymentRow struct {
	ID              string         `db:"id"`
	Stack           string         `db:"stack"`
	Commit          string         `db:"commit"`
	Status          Status         `db:"status"`
	ErrorCode       string         `db:"error_code"`
	Phase           Phase          `db:"phase"`
	StartedAt       time.Time      `db:"started_at"`
	FinishedAt      sql.NullTime   `db:"finished_at"`
	DetailsPayload  []byte         `db:"details_payload"`
	ChangesPayload  []byte         `db:"changes_payload"`
	LatestAttemptID sql.NullString `db:"latest_attempt_id"`
	AttemptCount    int            `db:"attempt_count"`
	IsSuperseded    bool           `db:"is_superseded"`
}

type attemptRow struct {
	ID             string       `db:"id"`
	DeploymentID   string       `db:"deployment_id"`
	Number         int          `db:"number"`
	Status         Status       `db:"status"`
	ErrorCode      string       `db:"error_code"`
	Phase          Phase        `db:"phase"`
	StartedAt      time.Time    `db:"started_at"`
	FinishedAt     sql.NullTime `db:"finished_at"`
	DetailsPayload []byte       `db:"details_payload"`
}

func detailsFromDeployment(d Deployment) deploymentDetails {
	return deploymentDetails{
		ErrorDiagnostic: d.ErrorDiagnostic, ApplyStatus: d.ApplyStatus,
		VerificationStatus: d.VerificationStatus, CleanupStatus: d.CleanupStatus,
		ActualStateStatus: d.ActualStateStatus, ObservedAt: d.ObservedAt,
		ComparisonBasis: d.ComparisonBasis, ComparisonStatus: d.ComparisonStatus,
		BasisDeploymentID: d.BasisDeploymentID, Summary: d.Summary,
		Resources: d.Resources, DiffAvailable: d.DiffAvailable,
	}
}

func marshalDeploymentPayloads(d Deployment) (string, string, error) {
	details, err := json.Marshal(detailsFromDeployment(d))
	if err != nil {
		return "", "", err
	}
	changes := d.Changes
	if changes == nil {
		changes = []Change{}
	}
	changesPayload, err := json.Marshal(changes)
	if err != nil {
		return "", "", err
	}
	return string(details), string(changesPayload), nil
}

func projectedStatus(status Status, isSuperseded bool) Status {
	if isSuperseded {
		return Superseded
	}
	return status
}

func (row deploymentRow) deployment() (Deployment, error) {
	var details deploymentDetails
	if err := json.Unmarshal(row.DetailsPayload, &details); err != nil {
		return Deployment{}, err
	}
	var changes []Change
	if err := json.Unmarshal(row.ChangesPayload, &changes); err != nil {
		return Deployment{}, err
	}
	result := Deployment{
		ID: row.ID, Stack: row.Stack, Commit: row.Commit, Status: projectedStatus(row.Status, row.IsSuperseded),
		StartedAt: row.StartedAt.UTC(), ErrorCode: row.ErrorCode,
		ErrorDiagnostic: details.ErrorDiagnostic, Phase: row.Phase,
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

func (row summaryRow) toSummary() (DeploymentSummary, error) {
	var details deploymentDetails
	if err := json.Unmarshal(row.DetailsPayload, &details); err != nil {
		return DeploymentSummary{}, err
	}
	item := DeploymentSummary{
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

func (row attemptRow) attempt() (Attempt, error) {
	var details deploymentDetails
	if err := json.Unmarshal(row.DetailsPayload, &details); err != nil {
		return Attempt{}, err
	}
	result := Attempt{
		ID: row.ID, DeploymentID: row.DeploymentID, Number: row.Number, Status: row.Status,
		StartedAt: row.StartedAt.UTC(), ErrorCode: row.ErrorCode,
		ErrorDiagnostic: details.ErrorDiagnostic, Phase: row.Phase,
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

func (s *Store) attempts(ctx context.Context, deploymentID string) ([]Attempt, error) {
	query, args, err := dialect.From("deployment_attempts").Prepared(true).
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
	result := make([]Attempt, 0, len(rows))
	for _, row := range rows {
		attempt, convertErr := row.attempt()
		if convertErr != nil {
			return nil, convertErr
		}
		result = append(result, attempt)
	}
	return result, nil
}

func (s *Store) getAttempt(ctx context.Context, id string) (Attempt, error) {
	query, args, err := dialect.From("deployment_attempts").Prepared(true).
		Select("id", "deployment_id", "number", "status", "error_code", "phase",
			"started_at", "finished_at", "details_payload").
		Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return Attempt{}, err
	}
	var row attemptRow
	if err = s.db.Get(ctx, &row, query, args...); err != nil {
		return Attempt{}, notFound(err)
	}
	return row.attempt()
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
func (s *Store) Get(ctx context.Context, id string) (Deployment, error) {
	var row deploymentRow
	columns := append([]any{}, summaryColumns...)
	columns = append(columns, "changes_payload")
	query, args, err := dialect.From("deployments").Prepared(true).
		Select(columns...).
		Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return Deployment{}, err
	}
	if err = s.db.Get(ctx, &row, query, args...); err != nil {
		return Deployment{}, notFound(err)
	}
	result, err := row.deployment()
	if err != nil {
		return Deployment{}, err
	}
	result.Attempts, err = s.attempts(ctx, id)
	return result, err
}

// Desired loads a deployment's safe desired definition for independent subscribers.
func (s *Store) Desired(ctx context.Context, id string) (compose.File, error) {
	query, args, err := dialect.From("deployments").Prepared(true).
		Select("desired_payload").Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return compose.File{}, err
	}
	desired, err := s.getPrepared(ctx, query, args...)
	return desired.Definition, notFound(err)
}

// Baseline returns only the last successfully committed effective desired state.
func (s *Store) Baseline(ctx context.Context, stack string) (Prepared, error) {
	query, args, err := dialect.From(goqu.T("desired_snapshots").As("snapshot")).Prepared(true).
		Join(goqu.T("deployments").As("deployment"),
			goqu.On(goqu.I("deployment.id").Eq(goqu.I("snapshot.deployment_id")))).
		Select(goqu.I("deployment.desired_payload")).
		Where(goqu.Ex{"snapshot.stack": stack, "deployment.status": Succeeded}).ToSQL()
	if err != nil {
		return Prepared{}, err
	}
	desired, err := s.getPrepared(ctx, query, args...)
	return desired, notFound(err)
}

func (s *Store) getPrepared(ctx context.Context, query string, args ...any) (Prepared, error) {
	var payload []byte
	if err := s.db.Get(ctx, &payload, query, args...); err != nil {
		return Prepared{}, err
	}
	var desired Prepared
	err := json.Unmarshal(payload, &desired)
	return desired, err
}

func (s *Store) baselineDeploymentID(ctx context.Context, stack string) (string, error) {
	var id string
	query, args, err := dialect.From("desired_snapshots").Prepared(true).
		Select("deployment_id").Where(goqu.Ex{"stack": stack}).ToSQL()
	if err != nil {
		return "", err
	}
	err = s.db.Get(ctx, &id, query, args...)
	return id, notFound(err)
}

func (s *Store) latest(ctx context.Context, stack string) (Deployment, Prepared, error) {
	var id string
	query, args, err := dialect.From("deployments").Prepared(true).Select("id").
		Where(goqu.Ex{"stack": stack}).Order(goqu.I("rowid").Desc()).Limit(1).ToSQL()
	if err != nil {
		return Deployment{}, Prepared{}, err
	}
	if err = s.db.Get(ctx, &id, query, args...); err != nil {
		return Deployment{}, Prepared{}, notFound(err)
	}
	deployment, err := s.Get(ctx, id)
	if err != nil {
		return Deployment{}, Prepared{}, err
	}
	prepared, err := s.prepared(ctx, id)
	return deployment, prepared, err
}

func (s *Store) legacyRunning(ctx context.Context, stack string) ([]Deployment, error) {
	dataset := dialect.From("deployments").Prepared(true).Select("id").
		Where(
			goqu.Ex{"status": Running},
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
	result := make([]Deployment, 0, len(ids))
	for _, id := range ids {
		deployment, getErr := s.Get(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		result = append(result, deployment)
	}
	return result, nil
}

func (s *Store) prepared(ctx context.Context, id string) (Prepared, error) {
	query, args, err := dialect.From("deployments").Prepared(true).
		Select("desired_payload").Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return Prepared{}, err
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

func (s *Store) insert(ctx context.Context, d Deployment, desired Prepared, attempt Attempt) error {
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
	query, args, err := dialect.Insert("deployments").Prepared(true).Rows(goqu.Record{
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

func (s *Store) insertAttempt(ctx context.Context, d Deployment, attempt Attempt) error {
	if err := s.insertAttemptRow(ctx, attempt); err != nil {
		return err
	}
	d.ErrorDiagnostic, d.ApplyStatus = "", StageRunning
	d.VerificationStatus, d.CleanupStatus = StagePending, StagePending
	d.ActualStateStatus, d.ObservedAt = ActualStateUnknown, nil
	details, _, err := marshalDeploymentPayloads(d)
	if err != nil {
		return err
	}
	query, args, err := dialect.Update("deployments").Prepared(true).Set(goqu.Record{
		"status": Running, "error_code": "", "phase": PhaseApply,
		"finished_at": nil, "details_payload": details,
	}).Where(
		goqu.Ex{"id": d.ID, "status": []Status{Failed, Interrupted}},
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

func (s *Store) insertAttemptRow(ctx context.Context, attempt Attempt) error {
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
	query, args, err := dialect.Insert("deployment_attempts").Prepared(true).Rows(goqu.Record{
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

func (s *Store) finish(ctx context.Context, d Deployment, attempt Attempt) error {
	if attempt.FinishedAt == nil || attempt.FinishedAt.IsZero() {
		return fmt.Errorf("terminal attempt requires finish time")
	}
	attemptDetails, err := json.Marshal(deploymentDetails{
		ErrorDiagnostic: attempt.ErrorDiagnostic, ApplyStatus: attempt.ApplyStatus,
		VerificationStatus: attempt.VerificationStatus, CleanupStatus: attempt.CleanupStatus,
		ActualStateStatus: attempt.ActualStateStatus, ObservedAt: attempt.ObservedAt,
	})
	if err != nil {
		return err
	}
	query, args, err := dialect.Update("deployment_attempts").Prepared(true).Set(goqu.Record{
		"status": attempt.Status, "error_code": attempt.ErrorCode, "phase": attempt.Phase,
		"finished_at": attempt.FinishedAt.UTC(), "details_payload": string(attemptDetails),
	}).Where(goqu.Ex{"id": attempt.ID, "deployment_id": d.ID, "status": Running}).ToSQL()
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
	d.VerificationStatus, d.CleanupStatus = attempt.VerificationStatus, attempt.CleanupStatus
	d.ActualStateStatus, d.ObservedAt = attempt.ActualStateStatus, attempt.ObservedAt
	details, _, err := marshalDeploymentPayloads(d)
	if err != nil {
		return err
	}
	query, args, err = dialect.Update("deployments").Prepared(true).Set(goqu.Record{
		"status": attempt.Status, "error_code": attempt.ErrorCode, "phase": attempt.Phase,
		"finished_at": attempt.FinishedAt.UTC(), "details_payload": details,
	}).Where(
		goqu.Ex{"id": d.ID, "status": Running},
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

func (s *Store) syncRunningAttemptWithTerminalDeployment(ctx context.Context, attempt Attempt) (bool, error) {
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

func (s *Store) saveBaseline(ctx context.Context, id string) error {
	source := dialect.From("deployments").Prepared(true).
		Select("stack", goqu.I("id").As("deployment_id")).
		Where(goqu.Ex{"id": id, "status": "succeeded"})
	query, args, err := dialect.Insert("desired_snapshots").Prepared(true).
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
