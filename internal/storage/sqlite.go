// Package storage owns the application SQLite connection and context transactor.
package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/Thiht/transactor"
	"github.com/Thiht/transactor/stdlib"
	_ "modernc.org/sqlite"
)

//go:embed migrations/0001_initial.sql
var initialSchema string

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
	if err = s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize SQLite: %w", err)
	}
	return s, nil
}

// Close releases connections after application workers have stopped.
func (s *Database) Close() error { return s.db.Close() }

func (s *Database) migrate(ctx context.Context) error {
	return s.WithinTransaction(ctx, func(ctx context.Context) error {
		db := s.Get(ctx)
		_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY, checksum TEXT NOT NULL)`)
		if err != nil {
			return err
		}
		var count int
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
			return err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(initialSchema)))
		if count != 0 {
			var existing string
			err = db.QueryRowContext(ctx,
				"SELECT checksum FROM schema_migrations WHERE version = '0001_initial'",
			).Scan(&existing)
			if err != nil {
				return err
			}
			if count != 1 || existing != checksum {
				return fmt.Errorf("database schema does not match 0001_initial; refusing startup")
			}
			return nil
		}
		if _, err = db.ExecContext(ctx, initialSchema); err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, "INSERT INTO schema_migrations VALUES ('0001_initial', ?)", checksum)
		return err
	})
}
