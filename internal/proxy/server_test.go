package proxy

import (
	"bufio"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"imap-proxy/internal/config"
)

// TestServerAccept verifies that the server accepts a connection and sends a greeting.
func TestServerAccept(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	cfg := &config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:0"}}
	srv := NewServer(cfg, slog.Default())
	go srv.Serve(l)
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", l.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read greeting: %v", err)
	}
	if !strings.Contains(line, "OK") {
		t.Errorf("expected greeting with OK, got: %q", line)
	}
}

// TestServerClose verifies that Close causes the server to stop accepting connections.
func TestServerClose(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	cfg := &config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:0"}}
	srv := NewServer(cfg, slog.Default())
	addr := l.Addr().String()

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(l)
	}()

	// Give the server a moment to start.
	time.Sleep(10 * time.Millisecond)

	if err := srv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not stop after Close")
	}

	// Verify no new connections are accepted.
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Error("expected dial to fail after server closed, but it succeeded")
	}
}
