package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"ro-imap-proxy/internal/config"
	"ro-imap-proxy/internal/imap"
)

// integrationEnv holds the common state for an integration test session.
type integrationEnv struct {
	clientConn net.Conn
	clientR    *bufio.Reader
	received   chan string // commands received by the fake upstream
}

// newIntegrationEnv creates a proxy session backed by a fake IMAP upstream.
// The fake upstream accepts LOGIN, echoes "tag OK" for regular commands,
// handles IDLE (sends "+", waits for DONE), and handles LOGOUT.
// All commands received by the upstream are sent to the received channel.
func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()

	clientConn, proxyConn := net.Pipe()
	upClient, upServer := net.Pipe()
	received := make(chan string, 100)

	// Fake upstream goroutine.
	go func() {
		defer upServer.Close()
		sr := bufio.NewReader(upServer)

		// Greeting (consumed by the injected DialUpstream).
		fmt.Fprint(upServer, "* OK Fake IMAP server ready\r\n")

		// LOGIN.
		line, err := sr.ReadString('\n')
		if err != nil {
			return
		}
		received <- strings.TrimRight(line, "\r\n")
		if strings.Contains(strings.ToUpper(line), "LOGIN") {
			fmt.Fprint(upServer, "proxy0 OK LOGIN completed\r\n")
		} else {
			fmt.Fprint(upServer, "proxy0 NO unexpected command\r\n")
		}

		// Post-auth command loop.
		for {
			line, err := sr.ReadString('\n')
			if err != nil {
				return
			}
			trimmed := strings.TrimRight(line, "\r\n")
			received <- trimmed
			parts := strings.SplitN(trimmed, " ", 2)
			tag := parts[0]

			upper := strings.ToUpper(trimmed)

			switch {
			case strings.Contains(upper, " IDLE"):
				fmt.Fprintf(upServer, "+ idling\r\n")
				// Wait for DONE.
				for {
					dl, err := sr.ReadString('\n')
					if err != nil {
						return
					}
					if strings.EqualFold(strings.TrimRight(dl, "\r\n"), "DONE") {
						fmt.Fprintf(upServer, "%s OK IDLE terminated\r\n", tag)
						break
					}
				}

			case strings.Contains(upper, " LOGOUT"):
				fmt.Fprintf(upServer, "* BYE server logging out\r\n")
				fmt.Fprintf(upServer, "%s OK LOGOUT completed\r\n", tag)
				return

			default:
				fmt.Fprintf(upServer, "%s OK completed\r\n", tag)
			}
		}
	}()

	cfg := testConfig()
	sess := NewSession(proxyConn, cfg, testLogger())
	sess.dialUpstream = func(acct *config.AccountConfig) (net.Conn, *bufio.Reader, error) {
		r := bufio.NewReader(upClient)
		// Consume greeting, like real DialUpstream does.
		if _, err := r.ReadString('\n'); err != nil {
			return nil, nil, err
		}
		return upClient, r, nil
	}

	go sess.Run()

	env := &integrationEnv{
		clientConn: clientConn,
		clientR:    bufio.NewReader(clientConn),
		received:   received,
	}

	// Set a generous deadline for all reads.
	clientConn.SetReadDeadline(time.Now().Add(10 * time.Second))

	return env
}

// login reads the greeting, sends LOGIN, and verifies success.
func (e *integrationEnv) login(t *testing.T) {
	t.Helper()

	// Read greeting.
	greeting := e.readLine(t)
	if !strings.Contains(greeting, "* OK ro-imap-proxy ready") {
		t.Fatalf("unexpected greeting: %q", greeting)
	}

	// Send LOGIN.
	e.send(t, "A001 LOGIN reader1 localpass1\r\n")

	// Drain LOGIN from upstream received channel.
	e.drainUpstream(t)

	// Read LOGIN OK.
	resp := e.readLine(t)
	if !strings.Contains(resp, "A001 OK LOGIN") {
		t.Fatalf("expected LOGIN OK, got: %q", resp)
	}
}

func (e *integrationEnv) send(t *testing.T, data string) {
	t.Helper()
	if _, err := fmt.Fprint(e.clientConn, data); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func (e *integrationEnv) readLine(t *testing.T) string {
	t.Helper()
	line, err := e.clientR.ReadString('\n')
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	return line
}

// expectUpstream waits for a command on the received channel containing substring.
func (e *integrationEnv) expectUpstream(t *testing.T, substring string) string {
	t.Helper()
	select {
	case cmd := <-e.received:
		if !strings.Contains(strings.ToUpper(cmd), strings.ToUpper(substring)) {
			t.Fatalf("expected upstream command containing %q, got: %q", substring, cmd)
		}
		return cmd
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for upstream command containing %q", substring)
		return ""
	}
}

// drainUpstream reads and discards one item from the received channel.
func (e *integrationEnv) drainUpstream(t *testing.T) {
	t.Helper()
	select {
	case <-e.received:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout draining upstream command")
	}
}

// noUpstream verifies nothing was sent to upstream within a short window.
func (e *integrationEnv) noUpstream(t *testing.T) {
	t.Helper()
	select {
	case cmd := <-e.received:
		t.Fatalf("unexpected upstream command: %q", cmd)
	case <-time.After(50 * time.Millisecond):
		// Good — nothing sent to upstream.
	}
}

// readUntilTagged reads response lines until a tagged response (starting with tag + space) is seen.
func (e *integrationEnv) readUntilTagged(t *testing.T, tag string) []string {
	t.Helper()
	var lines []string
	for {
		line := e.readLine(t)
		lines = append(lines, line)
		if strings.HasPrefix(line, tag+" ") {
			return lines
		}
	}
}

// folderListResponses are the LIST responses sent by the folder-filter fake upstream.
var folderListResponses = []string{
	`* LIST (\HasNoChildren) "/" "INBOX"`,
	`* LIST (\HasNoChildren) "/" "Sent"`,
	`* LIST (\HasNoChildren) "/" "Drafts"`,
	`* LIST (\HasChildren) "/" "Archive"`,
	`* LIST (\HasNoChildren) "/" "Archive/2024"`,
	`* LIST (\HasNoChildren) "/" "Trash"`,
	`* LIST (\HasNoChildren) "/" "Spam"`,
}

// newFolderFilterEnv creates a proxy session with a fake upstream that responds
// to LIST/LSUB with realistic folder listing responses. The modify function
// (if non-nil) can adjust the account config before the session starts.
func newFolderFilterEnv(t *testing.T, modify func(*config.AccountConfig)) *integrationEnv {
	t.Helper()

	clientConn, proxyConn := net.Pipe()
	upClient, upServer := net.Pipe()
	received := make(chan string, 100)

	// Fake upstream goroutine.
	go func() {
		defer upServer.Close()
		sr := bufio.NewReader(upServer)

		// Greeting.
		fmt.Fprint(upServer, "* OK Fake IMAP server ready\r\n")

		// LOGIN.
		line, err := sr.ReadString('\n')
		if err != nil {
			return
		}
		received <- strings.TrimRight(line, "\r\n")
		if strings.Contains(strings.ToUpper(line), "LOGIN") {
			fmt.Fprint(upServer, "proxy0 OK LOGIN completed\r\n")
		} else {
			fmt.Fprint(upServer, "proxy0 NO unexpected command\r\n")
		}

		// Post-auth command loop.
		for {
			line, err := sr.ReadString('\n')
			if err != nil {
				return
			}
			trimmed := strings.TrimRight(line, "\r\n")
			received <- trimmed
			parts := strings.SplitN(trimmed, " ", 2)
			tag := parts[0]

			upper := strings.ToUpper(trimmed)

			// Consume any literal data attached to this line.
			consumeLiteral := func() {
				n, _, hasLit := imap.ParseLiteral([]byte(line))
				if hasLit {
					io.CopyN(io.Discard, sr, n)
					// Read the trailing line after the literal.
					sr.ReadString('\n')
				}
			}

			switch {
			case strings.Contains(upper, " LIST"):
				for _, lr := range folderListResponses {
					fmt.Fprintf(upServer, "%s\r\n", lr)
				}
				fmt.Fprintf(upServer, "%s OK LIST completed\r\n", tag)

			case strings.Contains(upper, " LSUB"):
				for _, lr := range folderListResponses {
					lsub := strings.Replace(lr, "* LIST", "* LSUB", 1)
					fmt.Fprintf(upServer, "%s\r\n", lsub)
				}
				fmt.Fprintf(upServer, "%s OK LSUB completed\r\n", tag)

			case strings.Contains(upper, " APPEND"):
				consumeLiteral()
				fmt.Fprintf(upServer, "%s OK APPEND completed\r\n", tag)

			case strings.Contains(upper, " LOGOUT"):
				fmt.Fprintf(upServer, "* BYE server logging out\r\n")
				fmt.Fprintf(upServer, "%s OK LOGOUT completed\r\n", tag)
				return

			default:
				fmt.Fprintf(upServer, "%s OK completed\r\n", tag)
			}
		}
	}()

	cfg := testConfig()
	if modify != nil {
		modify(&cfg.Accounts[0])
	}
	sess := NewSession(proxyConn, cfg, testLogger())
	sess.dialUpstream = func(acct *config.AccountConfig) (net.Conn, *bufio.Reader, error) {
		r := bufio.NewReader(upClient)
		// Consume greeting.
		if _, err := r.ReadString('\n'); err != nil {
			return nil, nil, err
		}
		return upClient, r, nil
	}

	go sess.Run()

	env := &integrationEnv{
		clientConn: clientConn,
		clientR:    bufio.NewReader(clientConn),
		received:   received,
	}

	clientConn.SetReadDeadline(time.Now().Add(10 * time.Second))

	return env
}

// TestIntegrationFullSession tests a complete session lifecycle:
// connect → greeting → capability → login → select(→examine rewrite) →
// fetch → store(blocked) → logout
func TestIntegrationFullSession(t *testing.T) {
	env := newIntegrationEnv(t)
	defer env.clientConn.Close()

	// 1. Read greeting.
	greeting := env.readLine(t)
	if !strings.Contains(greeting, "* OK ro-imap-proxy ready") {
		t.Fatalf("unexpected greeting: %q", greeting)
	}

	// 2. CAPABILITY (pre-auth, handled locally).
	env.send(t, "A001 CAPABILITY\r\n")
	capLine := env.readLine(t)
	if !strings.Contains(capLine, "CAPABILITY IMAP4rev1") {
		t.Fatalf("expected CAPABILITY response, got: %q", capLine)
	}
	if !strings.Contains(capLine, "IDLE") {
		t.Fatalf("expected IDLE in capabilities, got: %q", capLine)
	}
	capOK := env.readLine(t)
	if !strings.Contains(capOK, "A001 OK") {
		t.Fatalf("expected CAPABILITY OK, got: %q", capOK)
	}

	// 3. LOGIN.
	env.send(t, "A002 LOGIN reader1 localpass1\r\n")
	env.drainUpstream(t) // drain upstream LOGIN
	loginResp := env.readLine(t)
	if !strings.Contains(loginResp, "A002 OK LOGIN") {
		t.Fatalf("expected LOGIN OK, got: %q", loginResp)
	}

	// 4. SELECT INBOX → rewritten to EXAMINE.
	env.send(t, "A003 SELECT INBOX\r\n")
	upCmd := env.expectUpstream(t, "EXAMINE")
	if strings.Contains(upCmd, "SELECT") {
		t.Fatalf("SELECT should have been rewritten to EXAMINE, got: %q", upCmd)
	}
	selResp := env.readLine(t)
	if !strings.Contains(selResp, "A003 OK") {
		t.Fatalf("expected SELECT/EXAMINE OK, got: %q", selResp)
	}

	// 5. FETCH (allowed, forwarded to upstream).
	env.send(t, "A004 FETCH 1:* (FLAGS)\r\n")
	env.expectUpstream(t, "FETCH")
	fetchResp := env.readLine(t)
	if !strings.Contains(fetchResp, "A004 OK") {
		t.Fatalf("expected FETCH OK, got: %q", fetchResp)
	}

	// 6. STORE (blocked, rejected locally).
	env.send(t, "A005 STORE 1 +FLAGS (\\Seen)\r\n")
	storeResp := env.readLine(t)
	if !strings.Contains(storeResp, "A005 NO") || !strings.Contains(storeResp, "not allowed") {
		t.Fatalf("expected STORE rejection, got: %q", storeResp)
	}
	env.noUpstream(t) // STORE must not reach upstream

	// 7. Verify session still works after a blocked command.
	env.send(t, "A006 NOOP\r\n")
	env.expectUpstream(t, "NOOP")
	noopResp := env.readLine(t)
	if !strings.Contains(noopResp, "A006 OK") {
		t.Fatalf("expected NOOP OK, got: %q", noopResp)
	}

	// 8. LOGOUT (handled locally by proxy, not forwarded to upstream).
	env.send(t, "A007 LOGOUT\r\n")
	bye := env.readLine(t)
	if !strings.Contains(bye, "BYE") {
		t.Fatalf("expected BYE, got: %q", bye)
	}
	logoutOK := env.readLine(t)
	if !strings.Contains(logoutOK, "A007 OK LOGOUT") {
		t.Fatalf("expected OK LOGOUT, got: %q", logoutOK)
	}
}

// TestIntegrationBlockedCommands tests ALL blocked commands from the spec.
func TestIntegrationBlockedCommands(t *testing.T) {
	blockedCmds := []struct {
		name string
		cmd  string
	}{
		{"STORE", "STORE 1 +FLAGS (\\Seen)"},
		{"COPY", "COPY 1 Trash"},
		{"MOVE", "MOVE 1 Trash"},
		{"DELETE", "DELETE MyFolder"},
		{"EXPUNGE", "EXPUNGE"},
		{"APPEND", "APPEND INBOX {10}"},
		{"CREATE", "CREATE NewFolder"},
		{"RENAME", "RENAME OldFolder NewFolder"},
		{"SUBSCRIBE", "SUBSCRIBE INBOX"},
		{"UNSUBSCRIBE", "UNSUBSCRIBE INBOX"},
		{"AUTHENTICATE", "AUTHENTICATE PLAIN"},
	}

	env := newIntegrationEnv(t)
	defer env.clientConn.Close()
	env.login(t)

	for i, tc := range blockedCmds {
		t.Run(tc.name, func(t *testing.T) {
			tag := fmt.Sprintf("B%03d", i+1)
			env.send(t, fmt.Sprintf("%s %s\r\n", tag, tc.cmd))

			resp := env.readLine(t)
			if !strings.Contains(resp, tag+" NO") {
				t.Fatalf("expected %s NO rejection, got: %q", tag, resp)
			}
			if !strings.Contains(resp, "not allowed") {
				t.Fatalf("expected 'not allowed' in rejection, got: %q", resp)
			}
		})
	}

	// Verify session is still alive after all blocked commands.
	env.send(t, "B999 NOOP\r\n")
	env.expectUpstream(t, "NOOP")
	noopResp := env.readLine(t)
	if !strings.Contains(noopResp, "B999 OK") {
		t.Fatalf("expected NOOP OK after blocked commands, got: %q", noopResp)
	}
}

// TestIntegrationUIDBlockedCommands tests blocked UID subcommands.
func TestIntegrationUIDBlockedCommands(t *testing.T) {
	blockedUIDs := []struct {
		name string
		cmd  string
	}{
		{"UID STORE", "UID STORE 1:* FLAGS (\\Seen)"},
		{"UID COPY", "UID COPY 1:* Trash"},
		{"UID MOVE", "UID MOVE 1:* Trash"},
		{"UID EXPUNGE", "UID EXPUNGE 1:*"},
	}

	env := newIntegrationEnv(t)
	defer env.clientConn.Close()
	env.login(t)

	for i, tc := range blockedUIDs {
		t.Run(tc.name, func(t *testing.T) {
			tag := fmt.Sprintf("U%03d", i+1)
			env.send(t, fmt.Sprintf("%s %s\r\n", tag, tc.cmd))

			resp := env.readLine(t)
			if !strings.Contains(resp, tag+" NO") {
				t.Fatalf("expected %s NO rejection, got: %q", tag, resp)
			}
			if !strings.Contains(resp, "not allowed") {
				t.Fatalf("expected 'not allowed' in rejection, got: %q", resp)
			}
		})
	}

	// Verify allowed UID subcommands still work.
	env.send(t, "U100 UID FETCH 1:* (FLAGS)\r\n")
	env.expectUpstream(t, "UID FETCH")
	fetchResp := env.readLine(t)
	if !strings.Contains(fetchResp, "U100 OK") {
		t.Fatalf("expected UID FETCH OK, got: %q", fetchResp)
	}
}

// TestIntegrationAllowedCommands tests various allowed commands pass through to upstream.
func TestIntegrationAllowedCommands(t *testing.T) {
	allowedCmds := []struct {
		name     string
		cmd      string
		upstream string // substring expected in the upstream command
	}{
		{"FETCH", "FETCH 1:* (FLAGS)", "FETCH"},
		{"LIST", `LIST "" *`, "LIST"},
		{"LSUB", `LSUB "" *`, "LSUB"},
		{"STATUS", "STATUS INBOX (MESSAGES)", "STATUS"},
		{"SEARCH", "SEARCH ALL", "SEARCH"},
		{"NOOP", "NOOP", "NOOP"},
		{"CAPABILITY", "CAPABILITY", "CAPABILITY"},
		{"CHECK", "CHECK", "CHECK"},
		{"CLOSE", "CLOSE", "CLOSE"},
		{"EXAMINE", "EXAMINE INBOX", "EXAMINE"},
		{"UID FETCH", "UID FETCH 1:* (FLAGS)", "UID FETCH"},
		{"UID SEARCH", "UID SEARCH ALL", "UID SEARCH"},
	}

	env := newIntegrationEnv(t)
	defer env.clientConn.Close()
	env.login(t)

	for i, tc := range allowedCmds {
		t.Run(tc.name, func(t *testing.T) {
			tag := fmt.Sprintf("D%03d", i+1)
			env.send(t, fmt.Sprintf("%s %s\r\n", tag, tc.cmd))

			env.expectUpstream(t, tc.upstream)
			resp := env.readLine(t)
			if !strings.Contains(resp, tag+" OK") {
				t.Fatalf("expected %s OK, got: %q", tag, resp)
			}
		})
	}

	// Also test that lowercase select is rewritten and forwarded.
	t.Run("lowercase select rewrite", func(t *testing.T) {
		env.send(t, "D100 select INBOX\r\n")
		upCmd := env.expectUpstream(t, "EXAMINE")
		if strings.Contains(strings.ToUpper(upCmd), "SELECT") {
			t.Fatalf("lowercase select should be rewritten to EXAMINE, got: %q", upCmd)
		}
		resp := env.readLine(t)
		if !strings.Contains(resp, "D100 OK") {
			t.Fatalf("expected OK for rewritten select, got: %q", resp)
		}
	})
}

// --- Folder filter integration tests ---

func TestIntegrationFolderAllowList(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.AllowedFolders = []string{"INBOX", "Sent"}
	})
	defer env.clientConn.Close()
	env.login(t)

	env.send(t, "A002 LIST \"\" *\r\n")
	env.drainUpstream(t)

	lines := env.readUntilTagged(t, "A002")

	var folders []string
	for _, line := range lines {
		if strings.HasPrefix(line, "* LIST") {
			folders = append(folders, line)
		}
	}
	if len(folders) != 2 {
		t.Fatalf("expected 2 folders, got %d: %v", len(folders), folders)
	}
	for _, f := range folders {
		if !strings.Contains(f, "INBOX") && !strings.Contains(f, "Sent") {
			t.Errorf("unexpected folder in response: %s", f)
		}
	}
}

func TestIntegrationFolderBlockList(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.BlockedFolders = []string{"Spam", "Trash"}
	})
	defer env.clientConn.Close()
	env.login(t)

	env.send(t, "A002 LIST \"\" *\r\n")
	env.drainUpstream(t)

	lines := env.readUntilTagged(t, "A002")

	var folders []string
	for _, line := range lines {
		if strings.HasPrefix(line, "* LIST") {
			folders = append(folders, line)
		}
	}
	// 7 total - 2 blocked = 5
	if len(folders) != 5 {
		t.Fatalf("expected 5 folders, got %d: %v", len(folders), folders)
	}
	for _, f := range folders {
		if strings.Contains(f, "\"Spam\"") || strings.Contains(f, "\"Trash\"") {
			t.Errorf("blocked folder in response: %s", f)
		}
	}
}

func TestIntegrationLsubFiltering(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.BlockedFolders = []string{"Spam"}
	})
	defer env.clientConn.Close()
	env.login(t)

	env.send(t, "A002 LSUB \"\" *\r\n")
	env.drainUpstream(t)

	lines := env.readUntilTagged(t, "A002")

	for _, line := range lines {
		if strings.HasPrefix(line, "* LSUB") && strings.Contains(line, "\"Spam\"") {
			t.Errorf("blocked folder in LSUB response: %s", line)
		}
	}

	// Count unblocked LSUB responses: 7 - 1 = 6
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "* LSUB") {
			count++
		}
	}
	if count != 6 {
		t.Fatalf("expected 6 LSUB responses, got %d", count)
	}
}

func TestIntegrationSelectBlockedFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.BlockedFolders = []string{"Trash"}
	})
	defer env.clientConn.Close()
	env.login(t)

	env.send(t, "A002 SELECT Trash\r\n")
	resp := env.readLine(t)
	if !strings.Contains(resp, "A002 NO") {
		t.Fatalf("expected NO for blocked SELECT, got: %q", resp)
	}
	env.noUpstream(t)
}

func TestIntegrationExamineBlockedFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.BlockedFolders = []string{"Trash"}
	})
	defer env.clientConn.Close()
	env.login(t)

	env.send(t, "A002 EXAMINE Trash\r\n")
	resp := env.readLine(t)
	if !strings.Contains(resp, "A002 NO") {
		t.Fatalf("expected NO for blocked EXAMINE, got: %q", resp)
	}
	env.noUpstream(t)
}

func TestIntegrationStatusBlockedFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.BlockedFolders = []string{"Trash"}
	})
	defer env.clientConn.Close()
	env.login(t)

	env.send(t, "A002 STATUS Trash (MESSAGES)\r\n")
	resp := env.readLine(t)
	if !strings.Contains(resp, "A002 NO") {
		t.Fatalf("expected NO for blocked STATUS, got: %q", resp)
	}
	env.noUpstream(t)
}

func TestIntegrationNoFilterAllPassThrough(t *testing.T) {
	env := newFolderFilterEnv(t, nil)
	defer env.clientConn.Close()
	env.login(t)

	env.send(t, "A002 LIST \"\" *\r\n")
	env.drainUpstream(t)

	lines := env.readUntilTagged(t, "A002")

	var folders []string
	for _, line := range lines {
		if strings.HasPrefix(line, "* LIST") {
			folders = append(folders, line)
		}
	}
	if len(folders) != 7 {
		t.Fatalf("expected 7 folders (no filter), got %d: %v", len(folders), folders)
	}
}

// --- Writable folders integration tests ---

func TestIntegrationSelectWritableFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.WritableFolders = []string{"Drafts"}
	})
	defer env.clientConn.Close()
	env.login(t)

	// SELECT on writable folder passes through as SELECT (not rewritten to EXAMINE).
	env.send(t, "A002 SELECT Drafts\r\n")
	upCmd := env.expectUpstream(t, "SELECT")
	if strings.Contains(strings.ToUpper(upCmd), "EXAMINE") {
		t.Fatalf("writable folder SELECT should NOT be rewritten to EXAMINE, got: %q", upCmd)
	}
	resp := env.readLine(t)
	if !strings.Contains(resp, "A002 OK") {
		t.Fatalf("expected SELECT OK, got: %q", resp)
	}
}

func TestIntegrationSelectNonWritableStillRewritten(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.WritableFolders = []string{"Drafts"}
	})
	defer env.clientConn.Close()
	env.login(t)

	// SELECT on non-writable folder still rewritten to EXAMINE.
	env.send(t, "A002 SELECT INBOX\r\n")
	upCmd := env.expectUpstream(t, "EXAMINE")
	if strings.Contains(strings.ToUpper(upCmd), "SELECT") {
		t.Fatalf("non-writable SELECT should be rewritten to EXAMINE, got: %q", upCmd)
	}
	resp := env.readLine(t)
	if !strings.Contains(resp, "A002 OK") {
		t.Fatalf("expected OK, got: %q", resp)
	}
}

func TestIntegrationStoreInWritableFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.WritableFolders = []string{"Drafts"}
	})
	defer env.clientConn.Close()
	env.login(t)

	// SELECT writable folder first.
	env.send(t, "A002 SELECT Drafts\r\n")
	env.expectUpstream(t, "SELECT")
	env.readLine(t) // OK

	// STORE should be allowed.
	env.send(t, "A003 STORE 1 +FLAGS (\\Seen)\r\n")
	env.expectUpstream(t, "STORE")
	resp := env.readLine(t)
	if !strings.Contains(resp, "A003 OK") {
		t.Fatalf("expected STORE OK in writable folder, got: %q", resp)
	}
}

func TestIntegrationStoreInNonWritableFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.WritableFolders = []string{"Drafts"}
	})
	defer env.clientConn.Close()
	env.login(t)

	// SELECT non-writable folder (rewritten to EXAMINE).
	env.send(t, "A002 SELECT INBOX\r\n")
	env.expectUpstream(t, "EXAMINE")
	env.readLine(t) // OK

	// STORE should be blocked.
	env.send(t, "A003 STORE 1 +FLAGS (\\Seen)\r\n")
	resp := env.readLine(t)
	if !strings.Contains(resp, "A003 NO") {
		t.Fatalf("expected STORE blocked in non-writable folder, got: %q", resp)
	}
	env.noUpstream(t)
}

func TestIntegrationUIDStoreInWritableFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.WritableFolders = []string{"Drafts"}
	})
	defer env.clientConn.Close()
	env.login(t)

	// SELECT writable folder.
	env.send(t, "A002 SELECT Drafts\r\n")
	env.expectUpstream(t, "SELECT")
	env.readLine(t) // OK

	// UID STORE should be allowed.
	env.send(t, "A003 UID STORE 1 +FLAGS (\\Seen)\r\n")
	env.expectUpstream(t, "UID STORE")
	resp := env.readLine(t)
	if !strings.Contains(resp, "A003 OK") {
		t.Fatalf("expected UID STORE OK in writable folder, got: %q", resp)
	}
}

func TestIntegrationAppendToWritableFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.WritableFolders = []string{"Drafts"}
	})
	defer env.clientConn.Close()
	env.login(t)

	// APPEND to writable folder with non-sync literal.
	// Combine command, literal data, and trailing line in one send to avoid
	// net.Pipe deadlock (proxy writes to upstream while test writes literal).
	msgBody := "Subject: hi\r\n\r\nHello\r\n"
	env.send(t, fmt.Sprintf("A002 APPEND Drafts {%d+}\r\n%s\r\n", len(msgBody), msgBody))

	env.expectUpstream(t, "APPEND")
	resp := env.readLine(t)
	if !strings.Contains(resp, "A002 OK") {
		t.Fatalf("expected APPEND OK for writable folder, got: %q", resp)
	}
}

func TestIntegrationAppendToNonWritableFolder(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.WritableFolders = []string{"Drafts"}
	})
	defer env.clientConn.Close()
	env.login(t)

	// APPEND to non-writable folder with non-sync literal — should be blocked.
	// Combine command and literal in one send to avoid net.Pipe deadlock
	// (proxy writes rejection while test writes literal data).
	msgBody := "Subject: hi\r\n\r\nHello\r\n"
	env.send(t, fmt.Sprintf("A002 APPEND INBOX {%d+}\r\n%s", len(msgBody), msgBody))

	resp := env.readLine(t)
	if !strings.Contains(resp, "A002 NO") {
		t.Fatalf("expected APPEND blocked for non-writable folder, got: %q", resp)
	}
	env.noUpstream(t)
}

func TestIntegrationWritableFolderOtherCommandsStillBlocked(t *testing.T) {
	env := newFolderFilterEnv(t, func(a *config.AccountConfig) {
		a.WritableFolders = []string{"Drafts"}
	})
	defer env.clientConn.Close()
	env.login(t)

	// SELECT writable folder.
	env.send(t, "A002 SELECT Drafts\r\n")
	env.expectUpstream(t, "SELECT")
	env.readLine(t) // OK

	// COPY, MOVE, DELETE, EXPUNGE should all still be blocked.
	blocked := []struct {
		name string
		cmd  string
	}{
		{"COPY", "COPY 1 INBOX"},
		{"MOVE", "MOVE 1 INBOX"},
		{"DELETE", "DELETE Drafts"},
		{"EXPUNGE", "EXPUNGE"},
		{"CREATE", "CREATE NewFolder"},
		{"RENAME", "RENAME Drafts NewDrafts"},
	}

	for i, tc := range blocked {
		t.Run(tc.name, func(t *testing.T) {
			tag := fmt.Sprintf("B%03d", i+1)
			env.send(t, fmt.Sprintf("%s %s\r\n", tag, tc.cmd))
			resp := env.readLine(t)
			if !strings.Contains(resp, tag+" NO") {
				t.Fatalf("expected %s blocked even in writable folder, got: %q", tc.name, resp)
			}
		})
	}
}

func TestIntegrationNoWritableFoldersDefaultReadOnly(t *testing.T) {
	env := newFolderFilterEnv(t, nil) // no writable folders
	defer env.clientConn.Close()
	env.login(t)

	// SELECT should be rewritten to EXAMINE.
	env.send(t, "A002 SELECT Drafts\r\n")
	upCmd := env.expectUpstream(t, "EXAMINE")
	if strings.Contains(strings.ToUpper(upCmd), "SELECT") {
		t.Fatalf("without writable folders, SELECT should be rewritten, got: %q", upCmd)
	}
	env.readLine(t) // OK

	// STORE should be blocked.
	env.send(t, "A003 STORE 1 +FLAGS (\\Seen)\r\n")
	resp := env.readLine(t)
	if !strings.Contains(resp, "A003 NO") {
		t.Fatalf("expected STORE blocked without writable folders, got: %q", resp)
	}
	env.noUpstream(t)
}
