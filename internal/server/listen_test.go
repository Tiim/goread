package server

import (
	"net"
	"testing"
)

func TestListenBindsToRequestedPort(t *testing.T) {
	ln, err := Listen(0)
	t.Cleanup(func() {
		if ln != nil {
			ln.Close()
		}
	})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	if addr.Port == 0 {
		t.Error("expected a nonzero port to be bound")
	}
}

func TestListenIncrementsWhenPortTaken(t *testing.T) {
	blocker, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to reserve a port for the test: %v", err)
	}
	defer blocker.Close()
	takenPort := blocker.Addr().(*net.TCPAddr).Port

	ln, err := Listen(takenPort)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	gotPort := ln.Addr().(*net.TCPAddr).Port
	if gotPort == takenPort {
		t.Errorf("Listen() bound to already-taken port %d", takenPort)
	}
	if gotPort <= takenPort {
		t.Errorf("Listen() port = %d, want > %d", gotPort, takenPort)
	}
}

func TestListenBindsToLocalhostOnly(t *testing.T) {
	ln, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Errorf("expected listener to bind to loopback address, got %v", addr.IP)
	}
}
