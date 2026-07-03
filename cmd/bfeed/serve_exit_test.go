package main

import (
	"net"
	"testing"
)

// When ListenAndServe cannot bind (port already in use), runServe must report
// failure (exit code 1), not success — otherwise systemd Restart=on-failure and
// container restart policies keyed on the exit code never restart a server that
// never listened.
func TestServeExitsNonZeroWhenBindFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer ln.Close()

	t.Setenv("BFEED_BASE_URL", "http://localhost")
	t.Setenv("BFEED_LISTEN_ADDR", ln.Addr().String()) // already bound → bind fails
	t.Setenv("BFEED_DATABASE_PATH", t.TempDir()+"/exit.db")
	t.Setenv("BFEED_LOG_FORMAT", "text")

	if code := runServe(); code != 1 {
		t.Fatalf("runServe with an unavailable bind address = %d, want 1", code)
	}
}
