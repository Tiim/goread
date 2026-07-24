package db

import (
	"database/sql"
	"fmt"
	"io"
	"os"
)

// Backup writes a consistent snapshot of the database to w. It uses SQLite's
// `VACUUM INTO`, rather than copying the on-disk file directly, so a backup
// taken while WAL journal_mode is active (see pragmas above) always reflects
// committed data instead of racing the checkpoint of the -wal file.
func Backup(sqlDB *sql.DB, w io.Writer) error {
	tmp, err := os.CreateTemp("", "goread-backup-*.sqlite")
	if err != nil {
		return fmt.Errorf("create backup temp file: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	// VACUUM INTO refuses to write to a file that already exists, so the
	// just-created empty temp file (needed only to reserve a unique path)
	// must be removed before it runs.
	if err := os.Remove(tmpPath); err != nil {
		return fmt.Errorf("remove backup temp placeholder: %w", err)
	}

	if _, err := sqlDB.Exec("VACUUM INTO ?", tmpPath); err != nil {
		return fmt.Errorf("vacuum into backup file: %w", err)
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("open backup temp file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("copy backup file: %w", err)
	}
	return nil
}
