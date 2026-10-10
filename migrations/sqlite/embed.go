// Package sqlitemigrations provides the embedded SQLite schema migrations.
package sqlitemigrations

import "embed"

// Files contains the SQLite migrations consumed by goose.
//
//go:embed *.sql
var Files embed.FS
