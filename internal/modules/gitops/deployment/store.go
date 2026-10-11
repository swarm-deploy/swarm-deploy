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

// Store provides context-aware deployment and snapshot queries.
type Store struct{ db *storage.Database }

// NewStore creates a deployment repository on the application database.
func NewStore(db *storage.Database) *Store { return &Store{db: db} }

// List returns a lightweight page without loading desired snapshots or change arrays.
func (s *Store) List(ctx context.Context, filter ListFilter) (Page, error) {
	columns := `id,stack,revision,status,json_extract(payload,'$.status'),started_at_ns,finished_at_ns,
	error_code,phase,
	coalesce(json_extract(payload,'$.apply_status'),''),
	coalesce(json_extract(payload,'$.verification_status'),''),
	coalesce(json_extract(payload,'$.cleanup_status'),''),
	coalesce(json_extract(payload,'$.actual_state_status'),'unknown'),
	json_extract(payload,'$.observed_at'),
	coalesce(json_extract(payload,'$.comparison_basis'),'successful_baseline'),
	coalesce(json_extract(payload,'$.comparison_status'),'unknown'),
	coalesce(json_extract(payload,'$.basis_deployment_id'),''),
	coalesce(json_extract(payload,'$.summary.added'),0),
	coalesce(json_extract(payload,'$.summary.changed'),0),
	coalesce(json_extract(payload,'$.summary.removed'),0),
	coalesce(json_extract(payload,'$.summary.redacted'),0),
	coalesce(json_extract(payload,'$.resources.services'),0),
	coalesce(json_extract(payload,'$.resources.configs'),0),
	coalesce(json_extract(payload,'$.resources.secrets'),0),
	coalesce(json_extract(payload,'$.resources.networks'),0),
	coalesce(json_extract(payload,'$.resources.volumes'),0),
	coalesce(json_extract(payload,'$.diff_available'),0)`
	dataset := goqu.Dialect("sqlite3").From("deployments").Prepared(true).Select(goqu.L(columns))
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
	rows, err := s.db.Get(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	items := make([]DeploymentSummary, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanSummary(rows)
		if scanErr != nil {
			return Page{}, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return Page{}, err
	}
	page := Page{Deployments: items}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = encodeCursor(last.StartedAt.UnixNano(), last.ID)
		page.Deployments = items[:limit]
	}
	return page, nil
}

type summaryScanner interface {
	// Scan reads one lightweight summary row.
	Scan(...any) error
}

func scanSummary(row summaryScanner) (DeploymentSummary, error) {
	var item DeploymentSummary
	var payloadStatus Status
	var started int64
	var finished sql.NullInt64
	var observed sql.NullString
	err := row.Scan(&item.ID, &item.Stack, &item.Commit, &item.Status, &payloadStatus, &started, &finished,
		&item.ErrorCode, &item.Phase, &item.ApplyStatus, &item.VerificationStatus, &item.CleanupStatus,
		&item.ActualStateStatus, &observed, &item.ComparisonBasis, &item.ComparisonStatus,
		&item.BasisDeploymentID, &item.Summary.Added, &item.Summary.Changed, &item.Summary.Removed,
		&item.Summary.Redacted, &item.Resources.Services, &item.Resources.Configs, &item.Resources.Secrets,
		&item.Resources.Networks, &item.Resources.Volumes, &item.DiffAvailable)
	if err != nil {
		return DeploymentSummary{}, err
	}
	if payloadStatus != item.Status {
		return DeploymentSummary{}, fmt.Errorf("%w: deployment %s column status %q payload status %q",
			ErrInconsistentDeployment, item.ID, item.Status, payloadStatus)
	}
	item.StartedAt = time.Unix(0, started).UTC()
	if finished.Valid {
		value := time.Unix(0, finished.Int64).UTC()
		item.FinishedAt = &value
	}
	if observed.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, observed.String)
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
	var persistedStatus Status
	var errorCode string
	var phase Phase
	var payload string
	err := s.db.Get(ctx).QueryRowContext(ctx, "SELECT status,error_code,phase,payload FROM deployments WHERE id=?", id).
		Scan(&persistedStatus, &errorCode, &phase, &payload)
	if err != nil {
		return Deployment{}, notFound(err)
	}
	var result Deployment
	if err = json.Unmarshal([]byte(payload), &result); err != nil {
		return Deployment{}, err
	}
	if result.ID != id || result.Status != persistedStatus || result.ErrorCode != errorCode || result.Phase != phase {
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
	desired, err := storage.GetJSON[Prepared](ctx, s.db.Get, "SELECT desired_payload FROM deployments WHERE id=?", id)
	return desired.Definition, notFound(err)
}

// Baseline returns only the last successfully committed effective desired state.
func (s *Store) Baseline(ctx context.Context, stack string) (Prepared, error) {
	desired, err := storage.GetJSON[Prepared](ctx, s.db.Get, "SELECT payload FROM desired_snapshots WHERE stack=?", stack)
	return desired, notFound(err)
}

func (s *Store) baselineDeploymentID(ctx context.Context, stack string) (string, error) {
	var id string
	err := s.db.Get(ctx).QueryRowContext(ctx, "SELECT deployment_id FROM desired_snapshots WHERE stack=?", stack).Scan(&id)
	return id, notFound(err)
}

func (s *Store) latestUnsuccessfulBasis(ctx context.Context, stack string) (Prepared, string, bool, error) {
	var id, payload string
	err := s.db.Get(ctx).QueryRowContext(ctx, `SELECT attempt.id,attempt.desired_payload
		FROM deployments AS attempt LEFT JOIN desired_snapshots AS snapshot ON snapshot.stack=attempt.stack
		LEFT JOIN deployments AS baseline ON baseline.id=snapshot.deployment_id
		WHERE attempt.stack=? AND attempt.status IN ('failed','interrupted')
		AND (baseline.id IS NULL OR attempt.rowid>baseline.rowid)
		ORDER BY attempt.rowid DESC LIMIT 1`, stack).Scan(&id, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Prepared{}, "", false, nil
	}
	if err != nil {
		return Prepared{}, "", false, err
	}
	var prepared Prepared
	if err = json.Unmarshal([]byte(payload), &prepared); err != nil {
		return Prepared{}, "", false, err
	}
	return prepared, id, true, nil
}

func (s *Store) hasUnsuccessfulAfterBaseline(ctx context.Context, stack string) (bool, error) {
	var retry bool
	err := s.db.Get(ctx).QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM deployments AS attempt
		WHERE attempt.stack=? AND attempt.status IN ('failed','interrupted')
		AND attempt.rowid > (
			SELECT baseline.rowid FROM deployments AS baseline
			JOIN desired_snapshots AS snapshot ON snapshot.deployment_id=baseline.id
			WHERE snapshot.stack=?
		)
	)`, stack, stack).Scan(&retry)
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
	_, err = s.db.Get(ctx).ExecContext(ctx, `INSERT INTO deployments
	(id,stack,revision,status,error_code,phase,started_at_ns,effective_digest,desired_payload,payload)
	VALUES (?,?,?,?,?,?,?,?,?,?)`, d.ID, d.Stack, d.Commit, d.Status, d.ErrorCode, d.Phase, d.StartedAt.UnixNano(),
		desired.Digest, string(safe), string(payload))
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
	result, err := s.db.Get(ctx).ExecContext(ctx, `UPDATE deployments
	SET status=?,error_code=?,phase=?,finished_at_ns=?,payload=? WHERE id=? AND status='running'`,
		d.Status, d.ErrorCode, d.Phase, d.FinishedAt.UnixNano(), string(payload), d.ID)
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
	_, err := s.db.Get(ctx).ExecContext(ctx, `INSERT INTO desired_snapshots
 (deployment_id,stack,effective_digest,payload)
 SELECT id,stack,effective_digest,desired_payload FROM deployments WHERE id=? AND status='succeeded'
 ON CONFLICT(stack) DO UPDATE SET deployment_id=excluded.deployment_id,
 effective_digest=excluded.effective_digest,payload=excluded.payload`, id)
	return err
}
