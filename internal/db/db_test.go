package db

import (
	"path/filepath"
	"testing"
)

func TestOpenAppliesPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	tests := []struct {
		pragma string
		want   string
	}{
		{"foreign_keys", "1"},
		{"journal_mode", "wal"},
		{"synchronous", "1"}, // NORMAL
	}
	for _, tt := range tests {
		var got string
		if err := sqlDB.QueryRow("PRAGMA " + tt.pragma).Scan(&got); err != nil {
			t.Fatalf("query pragma %s: %v", tt.pragma, err)
		}
		if got != tt.want {
			t.Errorf("pragma %s = %q, want %q", tt.pragma, got, tt.want)
		}
	}
}

func TestOpenCreatesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	for _, table := range []string{"feeds", "articles", "schema_migrations"} {
		var name string
		err := sqlDB.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Errorf("expected table %q to exist: %v", table, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := Open(path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	sqlDB.Close()

	sqlDB2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer sqlDB2.Close()

	var count int
	if err := sqlDB2.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 3 {
		t.Errorf("schema_migrations count = %d, want 3", count)
	}
}
