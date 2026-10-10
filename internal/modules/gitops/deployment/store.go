package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// ErrNotFound means the requested attempt or successful baseline does not exist.
var ErrNotFound = errors.New("deployment not found")

// ErrNotRunning prevents overwriting a terminal deployment outcome.
var ErrNotRunning = errors.New("deployment is not running")

// Store provides context-aware deployment and snapshot queries.
type Store struct{ db *storage.Database }

// NewStore creates a deployment repository on the application database.
func NewStore(db *storage.Database) *Store { return &Store{db: db} }

// List returns recent actual attempts, independently of Event History.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Deployment, error) {
	query := "SELECT payload FROM deployments"
	args := []any{}
	if filter.Stack != "" {
		query += " WHERE stack=?"
		args = append(args, filter.Stack)
	}
	limit := filter.Limit
	if limit < 1 || limit > 100 {
		limit = 20
	}
	query += " ORDER BY started_at_ns DESC,id LIMIT ?"
	args = append(args, limit)
	return storage.QueryJSON[Deployment](ctx, s.db.Get, query, args...)
}

// Get loads one attempt.
func (s *Store) Get(ctx context.Context, id string) (Deployment, error) {
	result, err := storage.GetJSON[Deployment](ctx, s.db.Get, "SELECT payload FROM deployments WHERE id=?", id)
	return result, notFound(err)
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
 (id,stack,revision,status,started_at_ns,effective_digest,desired_payload,payload)
 VALUES (?,?,?,?,?,?,?,?)`, d.ID, d.Stack, d.Commit, d.Status, d.StartedAt.UnixNano(),
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
 SET status=?,finished_at_ns=?,payload=? WHERE id=? AND status='running'`,
		d.Status, d.FinishedAt.UnixNano(), string(payload), d.ID)
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
