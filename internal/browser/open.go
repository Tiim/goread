// Package browser opens the user's default web browser at startup, per
// docs/spec.md's "Startup" step 4.
package browser

import (
	"os/exec"
	"runtime"
)

// Open launches the OS default browser at url using the platform-appropriate
// opener command. Callers should run this in a goroutine (or otherwise not
// wait on it) since it must never block server startup, per spec.
func Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
