package proxy

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"imap-proxy/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Listen: ":143"},
		Accounts: []config.AccountConfig{
			{
				LocalUser:      "reader1",
				LocalPassword:  "localpass1",
				RemoteHost:     "mail.example.com",
				RemotePort:     993,
				RemoteUser:     "realuser@example.com",
				RemotePassword: "realpass",
				RemoteTLS:      true,
			},
		},
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// readLine reads a line from a buffered reader with a timeout via deadline on the conn.
func readLine(r *bufio.Reader) (string, error) {
	return r.ReadString('\n')
}

func TestSessionGreeting(t *testing.T) {
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	cfg := testConfig()
	sess := NewSession(proxyConn, cfg, testLogger())

	go sess.Run()

	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(clientConn)
	line, err := readLine(r)
	if err != nil {
		t.Fatalf("read greeting: %v", err)
	}
	if line != "* OK imap-proxy ready\r\n" {
		t.Fatalf("unexpected greeting: %q", line)
	}
	clientConn.Close()
}

func TestSessionCapability(t *testing.T) {
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	sess := NewSession(proxyConn, testConfig(), testLogger())
	go sess.Run()

	r := bufio.NewReader(clientConn)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))

	// Read greeting.
	readLine(r)

	// Send CAPABILITY.
	fmt.Fprint(clientConn, "A001 CAPABILITY\r\n")

	line1, _ := readLine(r)
	if !strings.Contains(line1, "CAPABILITY IMAP4rev1") {
		t.Fatalf("unexpected capability response: %q", line1)
	}
	line2, _ := readLine(r)
	if line2 != "A001 OK CAPABILITY completed\r\n" {
		t.Fatalf("unexpected OK: %q", line2)
	}
	clientConn.Close()
}

func TestSessionNoop(t *testing.T) {
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	sess := NewSession(proxyConn, testConfig(), testLogger())
	go sess.Run()

	r := bufio.NewReader(clientConn)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))

	readLine(r) // greeting

	fmt.Fprint(clientConn, "A001 NOOP\r\n")
	line, _ := readLine(r)
	if line != "A001 OK NOOP completed\r\n" {
		t.Fatalf("unexpected NOOP response: %q", line)
	}
	clientConn.Close()
}

func TestSessionLogout(t *testing.T) {
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	sess := NewSession(proxyConn, testConfig(), testLogger())
	go sess.Run()

	r := bufio.NewReader(clientConn)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))

	readLine(r) // greeting

	fmt.Fprint(clientConn, "A001 LOGOUT\r\n")
	line1, _ := readLine(r)
	if !strings.Contains(line1, "BYE") {
		t.Fatalf("expected BYE, got: %q", line1)
	}
	line2, _ := readLine(r)
	if !strings.Contains(line2, "OK LOGOUT") {
		t.Fatalf("expected OK LOGOUT, got: %q", line2)
	}
}

// fakeUpstream creates a fake upstream IMAP server on one end of a net.Pipe.
// It returns the client-side conn+reader and runs the server in a goroutine.
// The server sends a greeting, accepts LOGIN, responds OK.
func fakeUpstream(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	upClient, upServer := net.Pipe()

	go func() {
		defer upServer.Close()
		sr := bufio.NewReader(upServer)
		// Send greeting.
		fmt.Fprint(upServer, "* OK Fake IMAP ready\r\n")
		// Read LOGIN.
		line, err := sr.ReadString('\n')
		if err != nil {
			return
		}
		if strings.Contains(strings.ToUpper(line), "LOGIN") {
			fmt.Fprint(upServer, "proxy0 OK LOGIN completed\r\n")
		} else {
			fmt.Fprint(upServer, "proxy0 NO unexpected command\r\n")
		}
		// After login, echo everything back with "OK" for simplicity.
		for {
			line, err := sr.ReadString('\n')
			if err != nil {
				return
			}
			// Extract tag from the forwarded command.
			parts := strings.SplitN(strings.TrimRight(line, "\r\n"), " ", 2)
			tag := parts[0]
			fmt.Fprintf(upServer, "%s OK completed\r\n", tag)
		}
	}()

	return upClient, bufio.NewReader(upClient)
}

// loginSession creates a session with a fake upstream injected, sends LOGIN, and returns
// the client conn and reader positioned after the LOGIN OK response.
func loginSession(t *testing.T) (net.Conn, *bufio.Reader, *Session) {
	t.Helper()
	clientConn, proxyConn := net.Pipe()

	cfg := testConfig()
	sess := NewSession(proxyConn, cfg, testLogger())

	// Inject fake upstream dialer.
	sess.dialUpstream = func(acct *config.AccountConfig) (net.Conn, *bufio.Reader, error) {
		conn, reader := fakeUpstream(t)
		// Consume the greeting, like real DialUpstream does.
		if _, err := reader.ReadString('\n'); err != nil {
			return nil, nil, err
		}
		return conn, reader, nil
	}

	go sess.Run()

	r := bufio.NewReader(clientConn)
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// Read greeting.
	greeting, err := readLine(r)
	if err != nil {
		t.Fatalf("read greeting: %v", err)
	}
	if !strings.Contains(greeting, "OK") {
		t.Fatalf("unexpected greeting: %q", greeting)
	}

	// Send LOGIN.
	fmt.Fprint(clientConn, "A001 LOGIN reader1 localpass1\r\n")

	line, err := readLine(r)
	if err != nil {
		t.Fatalf("read login response: %v", err)
	}
	if !strings.Contains(line, "OK LOGIN") {
		t.Fatalf("expected LOGIN OK, got: %q", line)
	}

	return clientConn, r, sess
}

func TestSessionLoginSuccess(t *testing.T) {
	clientConn, _, _ := loginSession(t)
	clientConn.Close()
}

func TestSessionLoginFail(t *testing.T) {
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	cfg := testConfig()
	sess := NewSession(proxyConn, cfg, testLogger())
	go sess.Run()

	r := bufio.NewReader(clientConn)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))

	readLine(r) // greeting

	// Wrong password.
	fmt.Fprint(clientConn, "A001 LOGIN reader1 wrongpass\r\n")
	line, _ := readLine(r)
	if !strings.Contains(line, "NO LOGIN") {
		t.Fatalf("expected NO LOGIN, got: %q", line)
	}
	clientConn.Close()
}

func TestSessionLoginUnknownUser(t *testing.T) {
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	cfg := testConfig()
	sess := NewSession(proxyConn, cfg, testLogger())
	go sess.Run()

	r := bufio.NewReader(clientConn)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))

	readLine(r) // greeting

	fmt.Fprint(clientConn, "A001 LOGIN unknown wrongpass\r\n")
	line, _ := readLine(r)
	if !strings.Contains(line, "NO LOGIN") {
		t.Fatalf("expected NO LOGIN, got: %q", line)
	}
	clientConn.Close()
}

func TestSessionPostAuthLogout(t *testing.T) {
	clientConn, r, _ := loginSession(t)
	defer clientConn.Close()

	fmt.Fprint(clientConn, "A002 LOGOUT\r\n")

	line1, err := readLine(r)
	if err != nil {
		t.Fatalf("read BYE: %v", err)
	}
	if !strings.Contains(line1, "BYE") {
		t.Fatalf("expected BYE, got: %q", line1)
	}
	line2, err := readLine(r)
	if err != nil {
		t.Fatalf("read OK LOGOUT: %v", err)
	}
	if !strings.Contains(line2, "A002 OK LOGOUT") {
		t.Fatalf("expected A002 OK LOGOUT, got: %q", line2)
	}
}

func TestSessionBlockedCommand(t *testing.T) {
	clientConn, r, _ := loginSession(t)
	defer clientConn.Close()

	// Send STORE (blocked command).
	fmt.Fprint(clientConn, "A002 STORE 1 +FLAGS (\\Seen)\r\n")

	line, err := readLine(r)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(line, "NO") || !strings.Contains(line, "not allowed") {
		t.Fatalf("expected NO rejection, got: %q", line)
	}
}

func TestSessionSelectRewrite(t *testing.T) {
	// We need a custom fake upstream that captures what it receives.
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	upClient, upServer := net.Pipe()
	received := make(chan string, 10)

	go func() {
		defer upServer.Close()
		sr := bufio.NewReader(upServer)
		// Send greeting.
		fmt.Fprint(upServer, "* OK Fake IMAP ready\r\n")
		// Read LOGIN.
		line, _ := sr.ReadString('\n')
		if strings.Contains(strings.ToUpper(line), "LOGIN") {
			fmt.Fprint(upServer, "proxy0 OK LOGIN completed\r\n")
		}
		// Read and record commands.
		for {
			line, err := sr.ReadString('\n')
			if err != nil {
				return
			}
			received <- line
			parts := strings.SplitN(strings.TrimRight(line, "\r\n"), " ", 2)
			tag := parts[0]
			fmt.Fprintf(upServer, "%s OK completed\r\n", tag)
		}
	}()

	cfg := testConfig()
	sess := NewSession(proxyConn, cfg, testLogger())
	sess.dialUpstream = func(acct *config.AccountConfig) (net.Conn, *bufio.Reader, error) {
		r := bufio.NewReader(upClient)
		// Consume the greeting, like real DialUpstream does.
		r.ReadString('\n')
		return upClient, r, nil
	}

	go sess.Run()

	r := bufio.NewReader(clientConn)
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))

	readLine(r) // greeting
	fmt.Fprint(clientConn, "A001 LOGIN reader1 localpass1\r\n")
	readLine(r) // LOGIN OK

	// Send SELECT INBOX.
	fmt.Fprint(clientConn, "A002 SELECT INBOX\r\n")

	// Read what upstream received.
	select {
	case got := <-received:
		if !strings.Contains(got, "EXAMINE") {
			t.Fatalf("expected EXAMINE upstream, got: %q", got)
		}
		if strings.Contains(got, "SELECT") {
			t.Fatalf("SELECT should have been rewritten, got: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for upstream command")
	}

	// Read OK response forwarded from upstream to client.
	line, _ := readLine(r)
	if !strings.Contains(line, "OK") {
		t.Fatalf("expected OK from upstream, got: %q", line)
	}
}

func TestSessionAllowedCommand(t *testing.T) {
	// Custom upstream that captures commands.
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	upClient, upServer := net.Pipe()
	received := make(chan string, 10)

	go func() {
		defer upServer.Close()
		sr := bufio.NewReader(upServer)
		fmt.Fprint(upServer, "* OK Fake IMAP ready\r\n")
		line, _ := sr.ReadString('\n')
		if strings.Contains(strings.ToUpper(line), "LOGIN") {
			fmt.Fprint(upServer, "proxy0 OK LOGIN completed\r\n")
		}
		for {
			line, err := sr.ReadString('\n')
			if err != nil {
				return
			}
			received <- line
			parts := strings.SplitN(strings.TrimRight(line, "\r\n"), " ", 2)
			tag := parts[0]
			fmt.Fprintf(upServer, "%s OK completed\r\n", tag)
		}
	}()

	cfg := testConfig()
	sess := NewSession(proxyConn, cfg, testLogger())
	sess.dialUpstream = func(acct *config.AccountConfig) (net.Conn, *bufio.Reader, error) {
		r := bufio.NewReader(upClient)
		// Consume the greeting, like real DialUpstream does.
		r.ReadString('\n')
		return upClient, r, nil
	}

	go sess.Run()

	r := bufio.NewReader(clientConn)
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))

	readLine(r) // greeting
	fmt.Fprint(clientConn, "A001 LOGIN reader1 localpass1\r\n")
	readLine(r) // LOGIN OK

	// Send FETCH (allowed).
	fmt.Fprint(clientConn, "A002 FETCH 1 (FLAGS)\r\n")

	select {
	case got := <-received:
		if !strings.Contains(got, "FETCH") {
			t.Fatalf("expected FETCH upstream, got: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for upstream command")
	}

	line, _ := readLine(r)
	if !strings.Contains(line, "OK") {
		t.Fatalf("expected OK, got: %q", line)
	}
}

func TestParseLoginArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		wantUser string
		wantPass string
		wantErr  bool
	}{
		{
			name:     "unquoted",
			args:     "user pass",
			wantUser: "user",
			wantPass: "pass",
		},
		{
			name:     "both quoted",
			args:     `"user" "pass"`,
			wantUser: "user",
			wantPass: "pass",
		},
		{
			name:     "quoted user unquoted pass",
			args:     `"user" pass`,
			wantUser: "user",
			wantPass: "pass",
		},
		{
			name:     "quoted with spaces",
			args:     `"user with spaces" pass`,
			wantUser: "user with spaces",
			wantPass: "pass",
		},
		{
			name:     "escaped quotes",
			args:     `"user\"name" "pass\"word"`,
			wantUser: `user"name`,
			wantPass: `pass"word`,
		},
		{
			name:    "empty",
			args:    "",
			wantErr: true,
		},
		{
			name:    "only user",
			args:    "user",
			wantErr: true,
		},
		{
			name:    "unterminated quote",
			args:    `"user`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, pass, err := parseLoginArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got user=%q pass=%q", user, pass)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if user != tt.wantUser {
				t.Errorf("user: got %q, want %q", user, tt.wantUser)
			}
			if pass != tt.wantPass {
				t.Errorf("pass: got %q, want %q", pass, tt.wantPass)
			}
		})
	}
}
