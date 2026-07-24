package appdir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDataDirCreatesDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("USERPROFILE", home)

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected data directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%q is not a directory", dir)
	}
	if filepath.Base(dir) != appName {
		t.Errorf("data dir base = %q, want %q", filepath.Base(dir), appName)
	}
}

func TestDataDirRespectsXDGDataHome(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("XDG_DATA_HOME only applies on Linux/BSD")
	}

	custom := t.TempDir()
	t.Setenv("XDG_DATA_HOME", custom)

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	want := filepath.Join(custom, appName)
	if dir != want {
		t.Errorf("DataDir() = %q, want %q", dir, want)
	}
}

func TestDataDirFallsBackToLocalShare(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("XDG fallback only applies on Linux/BSD")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	want := filepath.Join(home, ".local", "share", appName)
	if dir != want {
		t.Errorf("DataDir() = %q, want %q", dir, want)
	}
}
