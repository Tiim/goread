package db

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate applies any migration files that have not yet been recorded in the
// schema_migrations table, in ascending filename order. Migrations are named
// like "0001_init.sql"; the leading number is used as the migration's
// version.
func Migrate(sqlDB *sql.DB) error {
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY
	) STRICT;`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}

	type migration struct {
		version  int
		filename string
	}
	var migs []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return fmt.Errorf("migration file %q missing version prefix", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return fmt.Errorf("migration file %q has invalid version prefix: %w", name, err)
		}
		migs = append(migs, migration{version: version, filename: name})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for _, m := range migs {
		var applied bool
		if err := sqlDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`, m.version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if applied {
			continue
		}

		contents, err := migrationFiles.ReadFile("migrations/" + m.filename)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", m.filename, err)
		}

		tx, err := sqlDB.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(string(contents)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %q: %w", m.filename, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}

	return nil
}
