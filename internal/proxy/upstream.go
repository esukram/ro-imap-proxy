package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"

	"imap-proxy/internal/config"
)

// DialUpstream connects to the upstream IMAP server described by acct.
// It reads and validates the server greeting, then returns the connection
// and a buffered reader positioned after the greeting.
func DialUpstream(acct *config.AccountConfig) (net.Conn, *bufio.Reader, error) {
	return dialUpstream(acct, nil)
}

// dialUpstream is the internal implementation; tlsCfg overrides the TLS config when non-nil.
func dialUpstream(acct *config.AccountConfig, tlsCfg *tls.Config) (net.Conn, *bufio.Reader, error) {
	addr := net.JoinHostPort(acct.RemoteHost, fmt.Sprintf("%d", acct.RemotePort))

	makeTLSConfig := func() *tls.Config {
		if tlsCfg != nil {
			return tlsCfg
		}
		return &tls.Config{ServerName: acct.RemoteHost}
	}

	var conn net.Conn
	var r *bufio.Reader

	switch {
	case acct.RemoteTLS:
		c, err := tls.Dial("tcp", addr, makeTLSConfig())
		if err != nil {
			return nil, nil, fmt.Errorf("tls dial %s: %w", addr, err)
		}
		conn = c
		r = bufio.NewReader(conn)

	case acct.RemoteStartTLS:
		plain, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		pr := bufio.NewReader(plain)

		// Read initial greeting before STARTTLS negotiation.
		if _, err := pr.ReadString('\n'); err != nil {
			plain.Close()
			return nil, nil, fmt.Errorf("starttls: read greeting: %w", err)
		}

		// Request STARTTLS.
		if _, err := fmt.Fprintf(plain, "proxy0 STARTTLS\r\n"); err != nil {
			plain.Close()
			return nil, nil, fmt.Errorf("starttls: send command: %w", err)
		}

		// Read server response.
		resp, err := pr.ReadString('\n')
		if err != nil {
			plain.Close()
			return nil, nil, fmt.Errorf("starttls: read response: %w", err)
		}
		if !strings.Contains(resp, " OK") {
			plain.Close()
			return nil, nil, fmt.Errorf("starttls: server rejected: %s", strings.TrimRight(resp, "\r\n"))
		}

		// Upgrade to TLS. After this point, pr is discarded; the bufio.Reader
		// buffer should be empty since the server does not send TLS data until
		// the client initiates the handshake.
		tlsConn := tls.Client(plain, makeTLSConfig())
		if err := tlsConn.Handshake(); err != nil {
			tlsConn.Close()
			return nil, nil, fmt.Errorf("starttls: tls handshake: %w", err)
		}
		conn = tlsConn
		r = bufio.NewReader(conn)

	default:
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		conn = c
		r = bufio.NewReader(conn)
	}

	// Read and validate the (post-TLS) greeting line.
	greeting, err := r.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("read greeting: %w", err)
	}
	if !strings.HasPrefix(greeting, "* OK") && !strings.HasPrefix(greeting, "* PREAUTH") {
		conn.Close()
		return nil, nil, fmt.Errorf("unexpected greeting: %s", strings.TrimRight(greeting, "\r\n"))
	}

	return conn, r, nil
}

// quoteIMAPString wraps s in double quotes, escaping backslashes and double quotes per RFC 3501.
func quoteIMAPString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// LoginUpstream sends an IMAP LOGIN command to the upstream server using the
// remote credentials from acct and waits for a tagged response.
func LoginUpstream(conn net.Conn, reader *bufio.Reader, acct *config.AccountConfig) error {
	cmd := fmt.Sprintf("proxy0 LOGIN %s %s\r\n",
		quoteIMAPString(acct.RemoteUser),
		quoteIMAPString(acct.RemotePassword),
	)
	if _, err := fmt.Fprint(conn, cmd); err != nil {
		return fmt.Errorf("login: send command: %w", err)
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("login: read response: %w", err)
		}
		if strings.HasPrefix(line, "proxy0 ") {
			if strings.Contains(line, " OK") {
				return nil
			}
			return fmt.Errorf("login failed: %s", strings.TrimRight(line, "\r\n"))
		}
	}
}
