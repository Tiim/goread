// Package appdir locates the directory GoRead should store its data in,
// following the XDG Base Directory Specification on Linux/BSD and the
// platform-native convention elsewhere.
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const appName = "goread"

// DataDir returns the directory GoRead should store its persistent data
// (the SQLite database) in, creating it if it does not already exist.
//
//   - Linux/BSD: $XDG_DATA_HOME/goread, falling back to ~/.local/share/goread.
//   - macOS: ~/Library/Application Support/goread.
//   - Windows: %LOCALAPPDATA%\goread, falling back to %APPDATA%\goread.
func DataDir() (string, error) {
	dir, err := dataDirForOS()
	if err != nil {
		return "", fmt.Errorf("determine data directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data directory %q: %w", dir, err)
	}
	return dir, nil
}

func dataDirForOS() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, appName), nil
		}
		if dir := os.Getenv("APPDATA"); dir != "" {
			return filepath.Join(dir, appName), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "AppData", "Local", appName), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", appName), nil
	default:
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			return filepath.Join(dir, appName), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", appName), nil
	}
}
