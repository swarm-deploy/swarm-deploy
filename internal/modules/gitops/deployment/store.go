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

// ErrInconsistentDeployment means indexed deployment columns disagree with the persisted payload.
var ErrInconsistentDeployment = errors.New("deployment row and payload are inconsistent")

// ErrInvalidCursor means a list cursor is malformed or unsupported.
var ErrInvalidCursor = errors.New("invalid deployment cursor")

var dialect = goqu.Dialect("sqlite3")

// Store provides context-aware deployment and snapshot queries.
type Store struct{ db *storage.Database }

// NewStore creates a deployment repository on the application database.
func NewStore(db *storage.Database) *Store { return &Store{db: db} }

// summaryColumns builds the projection used by List, aliasing JSON-extracted
// fields so sqlx can scan them directly into summaryRow by `db` tag.
func summaryColumns() []any {
	return []any{
		"id", "stack", "revision", "status",
		goqu.L("json_extract(payload,'$.status')").As("payload_status"),
		"started_at_ns", "finished_at_ns", "error_code", "phase",
		goqu.L("coalesce(json_extract(payload,'$.apply_status'),'')").As("apply_status"),
		goqu.L("coalesce(json_extract(payload,'$.verification_status'),'')").As("verification_status"),
		goqu.L("coalesce(json_extract(payload,'$.cleanup_status'),'')").As("cleanup_status"),
		goqu.L("coalesce(json_extract(payload,'$.actual_state_status'),'unknown')").As("actual_state_status"),
		goqu.L("json_extract(payload,'$.observed_at')").As("observed_at"),
		goqu.L("coalesce(json_extract(payload,'$.comparison_basis'),'successful_baseline')").As("comparison_basis"),
		goqu.L("coalesce(json_extract(payload,'$.comparison_status'),'unknown')").As("comparison_status"),
		goqu.L("coalesce(json_extract(payload,'$.basis_deployment_id'),'')").As("basis_deployment_id"),
		goqu.L("coalesce(json_extract(payload,'$.summary.added'),0)").As("summary_added"),
		goqu.L("coalesce(json_extract(payload,'$.summary.changed'),0)").As("summary_changed"),
		goqu.L("coalesce(json_extract(payload,'$.summary.removed'),0)").As("summary_removed"),
		goqu.L("coalesce(json_extract(payload,'$.summary.redacted'),0)").As("summary_redacted"),
		goqu.L("coalesce(json_extract(payload,'$.resources.services'),0)").As("resources_services"),
		goqu.L("coalesce(json_extract(payload,'$.resources.configs'),0)").As("resources_configs"),
		goqu.L("coalesce(json_extract(payload,'$.resources.secrets'),0)").As("resources_secrets"),
		goqu.L("coalesce(json_extract(payload,'$.resources.networks'),0)").As("resources_networks"),
		goqu.L("coalesce(json_extract(payload,'$.resources.volumes'),0)").As("resources_volumes"),
		goqu.L("coalesce(json_extract(payload,'$.diff_available'),0)").As("diff_available"),
	}
}

// List returns a lightweight page without loading desired snapshots or change arrays.
func (s *Store) List(ctx context.Context, filter ListFilter) (Page, error) {
	dataset := dialect.From("deployments").Prepared(true).Select(summaryColumns()...)
	if filter.Stack != "" {
		dataset = dataset.Where(goqu.Ex{"stack": filter.Stack})
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
		dataset = dataset.Where(goqu.Or(
			goqu.C("started_at_ns").Lt(started),
			goqu.And(goqu.C("started_at_ns").Eq(started), goqu.C("id").Gt(id)),
		))
	}
	limit := filter.Limit
	if limit < 1 || limit > 100 {
		limit = 20
	}
	dataset = dataset.Order(goqu.I("started_at_ns").Desc(), goqu.I("id").Asc()).Limit(uint(limit + 1))
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

// summaryRow mirrors the aliased projection built by summaryColumns so sqlx
// can populate it directly via `db` tags.
type summaryRow struct {
	ID                 string            `db:"id"`
	Stack              string            `db:"stack"`
	Commit             string            `db:"revision"`
	Status             Status            `db:"status"`
	PayloadStatus      Status            `db:"payload_status"`
	StartedAtNS        int64             `db:"started_at_ns"`
	FinishedAtNS       sql.NullInt64     `db:"finished_at_ns"`
	ErrorCode          string            `db:"error_code"`
	Phase              Phase             `db:"phase"`
	ApplyStatus        StageStatus       `db:"apply_status"`
	VerificationStatus StageStatus       `db:"verification_status"`
	CleanupStatus      StageStatus       `db:"cleanup_status"`
	ActualStateStatus  ActualStateStatus `db:"actual_state_status"`
	ObservedAt         sql.NullString    `db:"observed_at"`
	ComparisonBasis    ComparisonBasis   `db:"comparison_basis"`
	ComparisonStatus   ComparisonStatus  `db:"comparison_status"`
	BasisDeploymentID  string            `db:"basis_deployment_id"`
	SummaryAdded       int               `db:"summary_added"`
	SummaryChanged     int               `db:"summary_changed"`
	SummaryRemoved     int               `db:"summary_removed"`
	SummaryRedacted    int               `db:"summary_redacted"`
	ResourcesServices  int               `db:"resources_services"`
	ResourcesConfigs   int               `db:"resources_configs"`
	ResourcesSecrets   int               `db:"resources_secrets"`
	ResourcesNetworks  int               `db:"resources_networks"`
	ResourcesVolumes   int               `db:"resources_volumes"`
	DiffAvailable      bool              `db:"diff_available"`
}

func (row summaryRow) toSummary() (DeploymentSummary, error) {
	if row.PayloadStatus != row.Status {
		return DeploymentSummary{}, fmt.Errorf("%w: deployment %s column status %q payload status %q",
			ErrInconsistentDeployment, row.ID, row.Status, row.PayloadStatus)
	}
	item := DeploymentSummary{
		ID:                 row.ID,
		Stack:              row.Stack,
		Commit:             row.Commit,
		Status:             row.Status,
		StartedAt:          time.Unix(0, row.StartedAtNS).UTC(),
		ErrorCode:          row.ErrorCode,
		Phase:              row.Phase,
		ApplyStatus:        row.ApplyStatus,
		VerificationStatus: row.VerificationStatus,
		CleanupStatus:      row.CleanupStatus,
		ActualStateStatus:  row.ActualStateStatus,
		ComparisonBasis:    row.ComparisonBasis,
		ComparisonStatus:   row.ComparisonStatus,
		BasisDeploymentID:  row.BasisDeploymentID,
		Summary: ChangeSummary{
			Added:    row.SummaryAdded,
			Changed:  row.SummaryChanged,
			Removed:  row.SummaryRemoved,
			Redacted: row.SummaryRedacted,
		},
		Resources: ResourceSummary{
			Services: row.ResourcesServices,
			Configs:  row.ResourcesConfigs,
			Secrets:  row.ResourcesSecrets,
			Networks: row.ResourcesNetworks,
			Volumes:  row.ResourcesVolumes,
		},
		DiffAvailable: row.DiffAvailable,
	}
	if row.FinishedAtNS.Valid {
		value := time.Unix(0, row.FinishedAtNS.Int64).UTC()
		item.FinishedAt = &value
	}
	if row.ObservedAt.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, row.ObservedAt.String)
		if parseErr != nil {
			return DeploymentSummary{}, parseErr
		}
		item.ObservedAt = &value
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
	var row struct {
		Status    Status `db:"status"`
		ErrorCode string `db:"error_code"`
		Phase     Phase  `db:"phase"`
		Payload   string `db:"payload"`
	}
	query, args, err := dialect.From("deployments").Prepared(true).
		Select("status", "error_code", "phase", "payload").
		Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return Deployment{}, err
	}
	if err = s.db.Get(ctx, &row, query, args...); err != nil {
		return Deployment{}, notFound(err)
	}
	var result Deployment
	if err = json.Unmarshal([]byte(row.Payload), &result); err != nil {
		return Deployment{}, err
	}
	if result.ID != id || result.Status != row.Status || result.ErrorCode != row.ErrorCode || result.Phase != row.Phase {
		return Deployment{}, fmt.Errorf(
			"%w: deployment %s indexed columns disagree with payload", ErrInconsistentDeployment, id,
		)
	}
	if result.Summary == (ChangeSummary{}) && len(result.Changes) > 0 {
		result.Summary, result.Resources = summarize(result.Changes)
	}
	return result, nil
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
	query, args, err := dialect.From("desired_snapshots").Prepared(true).
		Select("payload").Where(goqu.Ex{"stack": stack}).ToSQL()
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
	payload, err := json.Marshal(d)
	if err != nil {
		return err
	}
	safe, err := json.Marshal(desired)
	if err != nil {
		return err
	}
	query, args, err := dialect.Insert("deployments").Prepared(true).Rows(goqu.Record{
		"id":               d.ID,
		"stack":            d.Stack,
		"revision":         d.Commit,
		"status":           d.Status,
		"error_code":       d.ErrorCode,
		"phase":            d.Phase,
		"started_at_ns":    d.StartedAt.UnixNano(),
		"effective_digest": desired.Digest,
		"desired_payload":  string(safe),
		"payload":          string(payload),
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
	payload, err := json.Marshal(d)
	if err != nil {
		return err
	}
	query, args, err := dialect.Update("deployments").Prepared(true).Set(goqu.Record{
		"status":         d.Status,
		"error_code":     d.ErrorCode,
		"phase":          d.Phase,
		"finished_at_ns": d.FinishedAt.UnixNano(),
		"payload":        string(payload),
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
		Select("id", "stack", "effective_digest", "desired_payload").
		Where(goqu.Ex{"id": id, "status": "succeeded"})
	query, args, err := dialect.Insert("desired_snapshots").Prepared(true).
		Cols("deployment_id", "stack", "effective_digest", "payload").
		FromQuery(source).
		OnConflict(goqu.DoUpdate("stack", goqu.Record{
			"deployment_id":    goqu.I("excluded.deployment_id"),
			"effective_digest": goqu.I("excluded.effective_digest"),
			"payload":          goqu.I("excluded.payload"),
		})).
		ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}
