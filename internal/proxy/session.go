package proxy

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"

	"ro-imap-proxy/internal/config"
	"ro-imap-proxy/internal/imap"
)

// SessionState represents the current state of an IMAP session.
type SessionState int

const (
	StateGreeting SessionState = iota
	StateNotAuth
	StateAuth
	StateSelected
	StateIdle
)

// Session manages a single client connection to the proxy.
type Session struct {
	clientConn   net.Conn
	upstreamConn net.Conn
	clientR      *bufio.Reader
	upstreamR    *bufio.Reader
	state        SessionState
	account      *config.AccountConfig
	config       *config.Config
	logger       *slog.Logger

	// dialUpstream allows tests to inject a fake dialer.
	dialUpstream func(acct *config.AccountConfig) (net.Conn, *bufio.Reader, error)
}

// NewSession creates a new Session for the given client connection.
func NewSession(clientConn net.Conn, cfg *config.Config, logger *slog.Logger) *Session {
	return &Session{
		clientConn:   clientConn,
		clientR:      bufio.NewReader(clientConn),
		state:        StateGreeting,
		config:       cfg,
		logger:       logger,
		dialUpstream: DialUpstream,
	}
}

// Run executes the session lifecycle: greeting, pre-auth, post-auth, teardown.
func (s *Session) Run() {
	defer s.clientConn.Close()

	// 1. Send greeting.
	if _, err := fmt.Fprint(s.clientConn, "* OK ro-imap-proxy ready\r\n"); err != nil {
		s.logger.Error("failed to send greeting", "err", err)
		return
	}
	s.state = StateNotAuth

	// 2. Pre-auth loop.
	for s.state == StateNotAuth {
		line, err := s.clientR.ReadString('\n')
		if err != nil {
			s.logger.Info("client disconnected in pre-auth", "err", err)
			return
		}

		cmd, parseErr := imap.ParseCommand([]byte(line))
		if parseErr != nil {
			// Can't parse → try to extract a tag for the BAD response.
			tag := extractTag(line)
			fmt.Fprintf(s.clientConn, "%s BAD command not recognized\r\n", tag)
			continue
		}

		switch cmd.Verb {
		case "CAPABILITY":
			fmt.Fprintf(s.clientConn, "* CAPABILITY IMAP4rev1 IDLE LITERAL+\r\n")
			fmt.Fprintf(s.clientConn, "%s OK CAPABILITY completed\r\n", cmd.Tag)

		case "NOOP":
			fmt.Fprintf(s.clientConn, "%s OK NOOP completed\r\n", cmd.Tag)

		case "LOGOUT":
			fmt.Fprintf(s.clientConn, "* BYE ro-imap-proxy logging out\r\n")
			fmt.Fprintf(s.clientConn, "%s OK LOGOUT completed\r\n", cmd.Tag)
			return

		case "LOGIN":
			s.handleLogin(cmd)

		default:
			fmt.Fprintf(s.clientConn, "%s BAD command not recognized\r\n", cmd.Tag)
		}
	}

	// 3. Post-auth: bidirectional proxy.
	s.runPostAuth()
}

// handleLogin processes a LOGIN command during pre-auth.
func (s *Session) handleLogin(cmd imap.Command) {
	// Extract args after "LOGIN ".
	raw := string(cmd.Raw)
	raw = strings.TrimRight(raw, "\r\n")
	// Find the args portion: skip "tag LOGIN "
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) < 3 {
		fmt.Fprintf(s.clientConn, "%s NO LOGIN failed\r\n", cmd.Tag)
		return
	}
	args := parts[2] // everything after "tag LOGIN"

	user, pass, err := parseLoginArgs(args)
	if err != nil {
		s.logger.Warn("LOGIN parse error", "err", err)
		fmt.Fprintf(s.clientConn, "%s NO LOGIN failed\r\n", cmd.Tag)
		return
	}

	acct := s.config.LookupUser(user)
	if acct == nil {
		s.logger.Warn("LOGIN unknown user", "user", user)
		fmt.Fprintf(s.clientConn, "%s NO LOGIN failed\r\n", cmd.Tag)
		return
	}

	if acct.LocalPassword != pass {
		s.logger.Warn("LOGIN wrong password", "user", user)
		fmt.Fprintf(s.clientConn, "%s NO LOGIN failed\r\n", cmd.Tag)
		return
	}

	conn, reader, dialErr := s.dialUpstream(acct)
	if dialErr != nil {
		s.logger.Error("upstream dial failed", "err", dialErr)
		fmt.Fprintf(s.clientConn, "%s NO LOGIN failed\r\n", cmd.Tag)
		return
	}

	if loginErr := LoginUpstream(conn, reader, acct); loginErr != nil {
		s.logger.Error("upstream login failed", "err", loginErr)
		conn.Close()
		fmt.Fprintf(s.clientConn, "%s NO LOGIN failed\r\n", cmd.Tag)
		return
	}

	s.upstreamConn = conn
	s.upstreamR = reader
	s.account = acct
	s.state = StateAuth
	s.logger = s.logger.With("user", user)
	s.logger.Info("login successful")
	fmt.Fprintf(s.clientConn, "%s OK LOGIN completed\r\n", cmd.Tag)
}

// runPostAuth runs the bidirectional proxy after authentication.
func (s *Session) runPostAuth() {
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			s.clientConn.Close()
			s.upstreamConn.Close()
		})
	}
	defer cleanup()

	done := make(chan struct{})

	// Upstream→Client goroutine: line-based reading with optional LIST/LSUB filtering.
	go func() {
		defer func() {
			cleanup()
			close(done)
		}()
		for {
			line, err := s.upstreamR.ReadString('\n')
			if len(line) > 0 {
				filtered := false
				if s.account.HasFolderFilter() {
					if mailbox, ok := imap.ParseListResponse([]byte(line)); ok {
						if !s.account.FolderAllowed(mailbox) {
							filtered = true
						}
					}
				}

				if !filtered {
					if _, wErr := io.WriteString(s.clientConn, line); wErr != nil {
						s.logger.Debug("write to client failed", "err", wErr)
						return
					}
				}

				// Handle server-side literals.
				n, _, hasLiteral := imap.ParseLiteral([]byte(line))
				if hasLiteral {
					if filtered {
						if _, dErr := io.CopyN(io.Discard, s.upstreamR, n); dErr != nil {
							return
						}
					} else {
						if _, cErr := io.CopyN(s.clientConn, s.upstreamR, n); cErr != nil {
							s.logger.Debug("copy upstream literal failed", "err", cErr)
							return
						}
					}
				}
			}
			if err != nil {
				if err != io.EOF {
					s.logger.Debug("read from upstream failed", "err", err)
				}
				return
			}
		}
	}()

	// Client→Upstream goroutine (runs in current goroutine).
	s.clientToUpstream()
	cleanup()
	<-done
}

// clientToUpstream reads commands from the client, filters them, and forwards to upstream.
func (s *Session) clientToUpstream() {
	for {
		line, err := s.clientR.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				s.logger.Debug("read from client failed", "err", err)
			}
			return
		}

		cmd, parseErr := imap.ParseCommand([]byte(line))
		if parseErr != nil {
			// Forward unparseable lines as-is (could be continuation data).
			if _, wErr := fmt.Fprint(s.upstreamConn, line); wErr != nil {
				return
			}
			continue
		}

		// Handle IDLE specially.
		if cmd.Verb == "IDLE" {
			if err := s.handleIdle(line); err != nil {
				s.logger.Debug("IDLE handling error", "err", err)
				return
			}
			continue
		}

		// Handle LOGOUT in post-auth: respond locally and let cleanup close upstream.
		if cmd.Verb == "LOGOUT" {
			fmt.Fprintf(s.clientConn, "* BYE ro-imap-proxy logging out\r\n")
			fmt.Fprintf(s.clientConn, "%s OK LOGOUT completed\r\n", cmd.Tag)
			return
		}

		result := imap.Filter(cmd)
		switch result.Action {
		case imap.Allow:
			if s.folderBlocked(cmd) {
				fmt.Fprintf(s.clientConn, "%s NO folder not available\r\n", cmd.Tag)
				continue
			}
			if err := s.forwardWithLiterals([]byte(line)); err != nil {
				return
			}

		case imap.Block:
			s.logger.Warn("blocked command", "verb", cmd.Verb)
			fmt.Fprint(s.clientConn, result.RejectMsg)
			// If there's a non-synchronizing literal, consume and discard it.
			n, nonSync, ok := imap.ParseLiteral([]byte(line))
			if ok && nonSync {
				io.CopyN(io.Discard, s.clientR, n)
			}

		case imap.Rewrite:
			if s.folderBlocked(cmd) {
				fmt.Fprintf(s.clientConn, "%s NO folder not available\r\n", cmd.Tag)
				continue
			}
			s.logger.Debug("rewritten command", "verb", cmd.Verb)
			if err := s.forwardWithLiterals(result.Rewritten); err != nil {
				return
			}
		}
	}
}

// handleIdle handles the IDLE command exchange.
func (s *Session) handleIdle(line string) error {
	// Forward IDLE to upstream.
	if _, err := fmt.Fprint(s.upstreamConn, line); err != nil {
		return err
	}

	// The upstream→client goroutine forwards the "+" continuation
	// and any untagged responses (e.g. * N EXISTS) to the client.
	// We only need to wait for DONE from client and forward it.
	for {
		clientLine, err := s.clientR.ReadString('\n')
		if err != nil {
			return err
		}
		// Forward to upstream.
		if _, wErr := fmt.Fprint(s.upstreamConn, clientLine); wErr != nil {
			return wErr
		}
		// Check if this is DONE (case-insensitive).
		trimmed := strings.TrimRight(clientLine, "\r\n")
		if strings.EqualFold(trimmed, "DONE") {
			return nil
		}
	}
}

// forwardWithLiterals forwards a line to upstream and handles any literal data.
// For synchronizing literals, the upstream→client goroutine forwards the "+"
// continuation to the client. For non-synchronizing literals, the client sends
// data immediately. In both cases, we copy N bytes from client to upstream.
func (s *Session) forwardWithLiterals(line []byte) error {
	for {
		n, _, hasLiteral := imap.ParseLiteral(line)

		if _, err := s.upstreamConn.Write(line); err != nil {
			return err
		}

		if !hasLiteral {
			return nil
		}

		// Copy N literal bytes from client to upstream.
		if _, err := io.CopyN(s.upstreamConn, s.clientR, n); err != nil {
			return err
		}

		// Read next line (may be another literal continuation).
		nextLine, err := s.clientR.ReadString('\n')
		if err != nil {
			return err
		}
		line = []byte(nextLine)
	}
}

// folderBlocked checks if the command targets a folder that is hidden by the
// account's folder filter. Returns true if the command should be rejected.
func (s *Session) folderBlocked(cmd imap.Command) bool {
	if !s.account.HasFolderFilter() {
		return false
	}
	switch cmd.Verb {
	case "SELECT", "EXAMINE", "STATUS":
	default:
		return false
	}
	mailbox := extractCommandMailbox(cmd)
	if mailbox == "" {
		return false
	}
	return !s.account.FolderAllowed(mailbox)
}

// extractCommandMailbox extracts the mailbox name argument from commands
// like SELECT, EXAMINE, or STATUS.
func extractCommandMailbox(cmd imap.Command) string {
	raw := strings.TrimRight(string(cmd.Raw), "\r\n")
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) < 3 {
		return ""
	}
	mailbox, _, err := parseOneArg(parts[2])
	if err != nil {
		return ""
	}
	return mailbox
}

// parseLoginArgs parses the arguments to a LOGIN command.
// Handles: user pass, "user" "pass", "user with spaces" pass, etc.
func parseLoginArgs(args string) (user, pass string, err error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", "", fmt.Errorf("empty LOGIN args")
	}

	user, rest, err := parseOneArg(args)
	if err != nil {
		return "", "", fmt.Errorf("parsing username: %w", err)
	}

	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", fmt.Errorf("missing password")
	}

	pass, _, err = parseOneArg(rest)
	if err != nil {
		return "", "", fmt.Errorf("parsing password: %w", err)
	}

	return user, pass, nil
}

// parseOneArg extracts one token from s, handling quoted strings.
// Returns the token value and the remaining string.
func parseOneArg(s string) (token, rest string, err error) {
	if s[0] == '"' {
		// Quoted string: find the closing unescaped quote.
		var b strings.Builder
		i := 1
		for i < len(s) {
			if s[i] == '\\' && i+1 < len(s) && s[i+1] == '"' {
				b.WriteByte('"')
				i += 2
				continue
			}
			if s[i] == '"' {
				return b.String(), s[i+1:], nil
			}
			b.WriteByte(s[i])
			i++
		}
		return "", "", fmt.Errorf("unterminated quoted string")
	}

	// Unquoted: read until space or end.
	idx := strings.IndexByte(s, ' ')
	if idx < 0 {
		return s, "", nil
	}
	return s[:idx], s[idx+1:], nil
}

// extractTag tries to get a tag from a raw line for error responses.
func extractTag(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.IndexByte(line, ' '); idx > 0 {
		return line[:idx]
	}
	if line != "" {
		return line
	}
	return "*"
}
