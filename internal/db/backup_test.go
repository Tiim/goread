package db

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tiim/goread/internal/model"
)

func TestBackup_ProducesRestorableSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	feeds := NewFeedRepo(sqlDB)
	if err := feeds.Create(&model.Feed{Title: "Example", FeedURL: "https://example.com/feed"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var buf bytes.Buffer
	if err := Backup(sqlDB, &buf); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("Backup() wrote no data")
	}

	backupPath := filepath.Join(t.TempDir(), "backup.sqlite")
	if err := os.WriteFile(backupPath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write backup file: %v", err)
	}

	restored, err := Open(backupPath)
	if err != nil {
		t.Fatalf("Open(backup) error = %v", err)
	}
	defer restored.Close()

	var count int
	if err := restored.QueryRow("SELECT COUNT(*) FROM feeds WHERE title = 'Example'").Scan(&count); err != nil {
		t.Fatalf("query restored backup: %v", err)
	}
	if count != 1 {
		t.Errorf("restored feeds count = %d, want 1", count)
	}
}
