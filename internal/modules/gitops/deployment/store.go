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

// ErrNotFound means the requested attempt or successful baseline does not exist.
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

// FilterOptions returns stack and service names represented by deployment attempts.
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
}

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
		dataset = dataset.Where(goqu.Ex{"status": filter.Status})
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
		SELECT d.id, d."commit", d.status, d.started_at,
			json_extract(svc.value, '$.image') AS image
		FROM deployments AS d,
			json_each(d.desired_payload, '$.definition.compose.services') AS svc
		WHERE d.stack = ? AND json_extract(svc.value, '$.name') = ?
		ORDER BY d.started_at DESC, d.id ASC
		LIMIT ?`
	var rows []struct {
		ID        string    `db:"id"`
		Commit    string    `db:"commit"`
		Status    Status    `db:"status"`
		StartedAt time.Time `db:"started_at"`
		Image     string    `db:"image"`
	}
	if err := s.db.Select(ctx, &rows, query, stack, service, limit); err != nil {
		return nil, err
	}
	items := make([]ServiceDeployment, 0, len(rows))
	for _, row := range rows {
		items = append(items, ServiceDeployment{
			ID: row.ID, Commit: row.Commit, Status: row.Status,
			StartedAt: row.StartedAt.UTC(), Image: row.Image,
		})
	}
	return items, nil
}

type summaryRow struct {
	ID             string       `db:"id"`
	Stack          string       `db:"stack"`
	Commit         string       `db:"commit"`
	Status         Status       `db:"status"`
	ErrorCode      string       `db:"error_code"`
	Phase          Phase        `db:"phase"`
	StartedAt      time.Time    `db:"started_at"`
	FinishedAt     sql.NullTime `db:"finished_at"`
	DetailsPayload []byte       `db:"details_payload"`
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
	ID             string       `db:"id"`
	Stack          string       `db:"stack"`
	Commit         string       `db:"commit"`
	Status         Status       `db:"status"`
	ErrorCode      string       `db:"error_code"`
	Phase          Phase        `db:"phase"`
	StartedAt      time.Time    `db:"started_at"`
	FinishedAt     sql.NullTime `db:"finished_at"`
	DetailsPayload []byte       `db:"details_payload"`
	ChangesPayload []byte       `db:"changes_payload"`
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
		ID: row.ID, Stack: row.Stack, Commit: row.Commit, Status: row.Status,
		StartedAt: row.StartedAt.UTC(), ErrorCode: row.ErrorCode,
		ErrorDiagnostic: details.ErrorDiagnostic, Phase: row.Phase,
		ApplyStatus: details.ApplyStatus, VerificationStatus: details.VerificationStatus,
		CleanupStatus: details.CleanupStatus, ActualStateStatus: details.ActualStateStatus,
		ObservedAt: details.ObservedAt, ComparisonBasis: details.ComparisonBasis,
		ComparisonStatus: details.ComparisonStatus, BasisDeploymentID: details.BasisDeploymentID,
		Summary: details.Summary, Resources: details.Resources, Changes: changes,
		DiffAvailable: details.DiffAvailable,
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
		Status:             row.Status,
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
	}
	if row.FinishedAt.Valid {
		value := row.FinishedAt.Time.UTC()
		item.FinishedAt = &value
	}
	return item, nil
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

// Get loads one attempt.
func (s *Store) Get(ctx context.Context, id string) (Deployment, error) {
	var row deploymentRow
	query, args, err := dialect.From("deployments").Prepared(true).
		Select(
			"id", "stack", "commit", "status", "error_code", "phase", "started_at", "finished_at",
			"details_payload", "changes_payload",
		).
		Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return Deployment{}, err
	}
	if err = s.db.Get(ctx, &row, query, args...); err != nil {
		return Deployment{}, notFound(err)
	}
	return row.deployment()
}

// Desired loads an attempt's safe definition for independent subscribers.
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

func (s *Store) latestUnsuccessfulBasis(ctx context.Context, stack string) (Prepared, string, bool, error) {
	var row struct {
		ID      string `db:"id"`
		Payload string `db:"desired_payload"`
	}
	attempt := goqu.T("deployments").As("attempt")
	snapshot := goqu.T("desired_snapshots").As("snapshot")
	baseline := goqu.T("deployments").As("baseline")
	query, args, err := dialect.From(attempt).Prepared(true).
		LeftJoin(snapshot, goqu.On(goqu.I("snapshot.stack").Eq(goqu.I("attempt.stack")))).
		LeftJoin(baseline, goqu.On(goqu.I("baseline.id").Eq(goqu.I("snapshot.deployment_id")))).
		Select(goqu.I("attempt.id"), goqu.I("attempt.desired_payload")).
		Where(
			goqu.I("attempt.stack").Eq(stack),
			goqu.I("attempt.status").In("failed", "interrupted"),
			goqu.Or(
				goqu.I("baseline.id").IsNull(),
				goqu.I("attempt.rowid").Gt(goqu.I("baseline.rowid")),
			),
		).
		Order(goqu.I("attempt.rowid").Desc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return Prepared{}, "", false, err
	}
	err = s.db.Get(ctx, &row, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return Prepared{}, "", false, nil
	}
	if err != nil {
		return Prepared{}, "", false, err
	}
	var prepared Prepared
	if err = json.Unmarshal([]byte(row.Payload), &prepared); err != nil {
		return Prepared{}, "", false, err
	}
	return prepared, row.ID, true, nil
}

func (s *Store) hasUnsuccessfulAfterBaseline(ctx context.Context, stack string) (bool, error) {
	var retry bool
	baselineRowID := dialect.From(goqu.T("deployments").As("baseline")).Prepared(true).
		Join(goqu.T("desired_snapshots").As("snapshot"),
			goqu.On(goqu.I("snapshot.deployment_id").Eq(goqu.I("baseline.id")))).
		Select(goqu.I("baseline.rowid")).
		Where(goqu.Ex{"snapshot.stack": stack})
	exists := dialect.From(goqu.T("deployments").As("attempt")).Prepared(true).
		Select(goqu.L("1")).
		Where(
			goqu.I("attempt.stack").Eq(stack),
			goqu.I("attempt.status").In("failed", "interrupted"),
			goqu.I("attempt.rowid").Gt(baselineRowID),
		)
	query, args, err := dialect.From().Prepared(true).Select(goqu.L("EXISTS ?", exists)).ToSQL()
	if err != nil {
		return false, err
	}
	err = s.db.Get(ctx, &retry, query, args...)
	return retry, err
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Store) insert(ctx context.Context, d Deployment, desired Prepared) error {
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
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

func (s *Store) finish(ctx context.Context, d Deployment) error {
	if d.FinishedAt == nil {
		return fmt.Errorf("terminal deployment requires finish time")
	}
	if d.FinishedAt.IsZero() {
		return fmt.Errorf("deployment finish time is required")
	}
	details, changes, err := marshalDeploymentPayloads(d)
	if err != nil {
		return err
	}
	query, args, err := dialect.Update("deployments").Prepared(true).Set(goqu.Record{
		"status": d.Status, "error_code": d.ErrorCode, "phase": d.Phase,
		"finished_at": d.FinishedAt.UTC(), "details_payload": details, "changes_payload": changes,
	}).Where(goqu.Ex{"id": d.ID, "status": "running"}).ToSQL()
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
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
