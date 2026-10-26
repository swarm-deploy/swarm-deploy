package storage

import (
	"context"
	"database/sql"

	txsqlx "github.com/Thiht/transactor/sqlx"
	"github.com/jmoiron/sqlx"
)

// Database owns one pool and one context-aware transaction manager.
type Database struct {
	db         *sqlx.DB
	get        txsqlx.DBGetter
	transactor *txsqlx.Transactor
}

// WithinTransaction runs fn inside the active transaction carried by ctx, or
// opens a new transaction when ctx is not transactional.
func (s *Database) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if s.transactor.IsWithinTransaction(ctx) {
		return fn(ctx)
	}
	return s.transactor.WithinTransaction(ctx, fn)
}

// Get executes a query expected to return at most one row and scans it into dest
// using the active transaction or the shared database pool. dest is typically a
// pointer to a struct with `db` tags, or a pointer to a single scalar value.
func (s *Database) Get(ctx context.Context, dest any, query string, args ...any) error {
	return s.get(ctx).GetContext(ctx, dest, query, args...)
}

// Select executes a query and scans all rows into dest using the active transaction
// or the shared database pool. dest must be a pointer to a slice, whose element type
// is typically a struct with `db` tags, or a single scalar value.
func (s *Database) Select(ctx context.Context, dest any, query string, args ...any) error {
	return s.get(ctx).SelectContext(ctx, dest, query, args...)
}

// Exec executes a statement using the active transaction or the shared database pool.
func (s *Database) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.get(ctx).ExecContext(ctx, query, args...)
}

// Close releases connections after application workers have stopped.
func (s *Database) Close() error { return s.db.Close() }
