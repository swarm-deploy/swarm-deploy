// Package storage owns the application SQLite connection and context transactor.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	txsqlx "github.com/Thiht/transactor/sqlx"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	sqlitemigrations "github.com/swarm-deploy/swarm-deploy/migrations/sqlite"
	_ "modernc.org/sqlite"
)

const maxConnections = 4

// Database owns one pool and one context-aware transaction manager.
type Database struct {
	db  *sqlx.DB
	get txsqlx.DBGetter
	// Transactor propagates transaction boundaries through context and keeps
	// nested transactions isolated using SQLite savepoints.
	*txsqlx.Transactor
}

// Open initializes the database on a local persistent volume.
func Open(ctx context.Context, dataDir string) (*Database, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	path, err := filepath.Abs(filepath.Join(dataDir, "swarm-deploy.sqlite"))
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create database file: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	// DSN pragmas run on EVERY connection, including replacements in the pool.
	u.RawQuery = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(FULL)&_txlock=immediate"
	db, err := sqlx.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)
	tr, get := txsqlx.NewTransactor(db, txsqlx.NestedTransactionsSavepoints)
	s := &Database{db: db, get: get, Transactor: tr}
	migrations, err := goose.NewProvider(
		goose.DialectSQLite3,
		db.DB,
		sqlitemigrations.Files,
	)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize SQLite migrations: %w", err)
	}
	if _, err = migrations.Up(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize SQLite: %w", err)
	}
	return s, nil
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
