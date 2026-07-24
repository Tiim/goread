// Package db provides SQLite database initialization and access for GoRead.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// pragmas configures SQLite according to the application's durability and
// concurrency requirements. They are applied once on the single retained
// connection (see Open), since some PRAGMAs are per-connection.
var pragmas = []string{
	"PRAGMA foreign_keys = ON;",
	"PRAGMA busy_timeout = 5000;",
	"PRAGMA journal_mode = WAL;",
	"PRAGMA synchronous = NORMAL;",
}

// Open opens (creating if necessary) the SQLite database at path, applies
// the required PRAGMAs, and runs any pending schema migrations.
//
// The returned *sql.DB is restricted to a single connection: SQLite only
// supports one writer at a time, and several of the PRAGMAs above are
// per-connection settings that must be reapplied if a new connection were
// opened.
func Open(path string) (*sql.DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("apply pragma %q: %w", p, err)
		}
	}

	if err := Migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	return sqlDB, nil
}
