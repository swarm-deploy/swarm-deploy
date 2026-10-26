package storage

import (
	"context"
	"database/sql"

	txsqlx "github.com/Thiht/transactor/sqlx"
	"github.com/jmoiron/sqlx"
)

// Database provides context-aware database operations.
type Database interface {
	// WithinTransaction runs fn inside the active transaction carried by ctx, or
	// opens a new transaction when ctx is not transactional.
	WithinTransaction(ctx context.Context, fn func(context.Context) error) error
	// Get executes a query expected to return at most one row and scans it into dest.
	Get(ctx context.Context, dest any, query string, args ...any) error
	// Select executes a query and scans all rows into dest.
	Select(ctx context.Context, dest any, query string, args ...any) error
	// Exec executes a statement using the active transaction or the shared database pool.
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	// Close releases database connections.
	Close() error
}

// SQLXDatabase owns one SQLX pool and one context-aware transaction manager.
type SQLXDatabase struct {
	db         *sqlx.DB
	get        txsqlx.DBGetter
	transactor *txsqlx.Transactor
}

var _ Database = (*SQLXDatabase)(nil)

// WithinTransaction runs fn inside the active transaction carried by ctx, or
// opens a new transaction when ctx is not transactional.
func (s *SQLXDatabase) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if s.transactor.IsWithinTransaction(ctx) {
		return fn(ctx)
	}
	return s.transactor.WithinTransaction(ctx, fn)
}

// Get executes a query expected to return at most one row and scans it into dest
// using the active transaction or the shared database pool. dest is typically a
// pointer to a struct with `db` tags, or a pointer to a single scalar value.
func (s *SQLXDatabase) Get(ctx context.Context, dest any, query string, args ...any) error {
	return s.get(ctx).GetContext(ctx, dest, query, args...)
}

// Select executes a query and scans all rows into dest using the active transaction
// or the shared database pool. dest must be a pointer to a slice, whose element type
// is typically a struct with `db` tags, or a single scalar value.
func (s *SQLXDatabase) Select(ctx context.Context, dest any, query string, args ...any) error {
	return s.get(ctx).SelectContext(ctx, dest, query, args...)
}

// Exec executes a statement using the active transaction or the shared database pool.
func (s *SQLXDatabase) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.get(ctx).ExecContext(ctx, query, args...)
}

// Close releases connections after application workers have stopped.
func (s *SQLXDatabase) Close() error { return s.db.Close() }
