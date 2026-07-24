// Package server contains the HTTP server for GoRead.
package server

import (
	"fmt"
	"net"
)

// DefaultStartPort is the first port GoRead attempts to bind to.
const DefaultStartPort = 8080

// MaxPortAttempts bounds how many ports are tried before giving up.
const MaxPortAttempts = 1000

// Listen binds to "localhost" starting at startPort, incrementing the port
// number until an available one is found. It returns the bound listener so
// the caller can read back the actual port in use.
func Listen(startPort int) (net.Listener, error) {
	var lastErr error
	for port := startPort; port < startPort+MaxPortAttempts; port++ {
		addr := fmt.Sprintf("localhost:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("no available port found starting at %d: %w", startPort, lastErr)
}
