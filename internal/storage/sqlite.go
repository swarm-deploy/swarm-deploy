// Package storage owns the application SQLite connection and context transactor.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/Thiht/transactor"
	"github.com/Thiht/transactor/stdlib"
	"github.com/pressly/goose/v3"
	sqlitemigrations "github.com/swarm-deploy/swarm-deploy/migrations/sqlite"
	_ "modernc.org/sqlite"
)

const maxConnections = 4

// Database owns one pool and one context-aware transaction manager.
type Database struct {
	db *sql.DB
	// Transactor propagates transaction boundaries through context.
	transactor.Transactor
	// Get selects the active transaction or the shared database pool.
	Get stdlib.DBGetter
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
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)
	tr, get := stdlib.NewTransactor(db, stdlib.NestedTransactionsSavepoints)
	s := &Database{db: db, Transactor: tr, Get: get}
	migrations, err := goose.NewProvider(
		goose.DialectSQLite3,
		db,
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

// Close releases connections after application workers have stopped.
func (s *Database) Close() error { return s.db.Close() }
