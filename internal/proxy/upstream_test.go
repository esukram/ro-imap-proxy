package proxy

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"ro-imap-proxy/internal/config"
)

// generateTestTLSConfigs creates a self-signed certificate and returns a server
// TLS config and an InsecureSkipVerify client TLS config for use in tests.
func generateTestTLSConfigs(t *testing.T) (serverCfg, clientCfg *tls.Config) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	cert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}

	serverCfg = &tls.Config{Certificates: []tls.Certificate{cert}}
	clientCfg = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test only
	return serverCfg, clientCfg
}

func TestDialUpstreamTLS(t *testing.T) {
	serverTLS, clientTLS := generateTestTLSConfigs(t)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- fmt.Errorf("accept: %w", err)
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "* OK TLS server ready\r\n")
		errCh <- nil
	}()

	addr := ln.Addr().(*net.TCPAddr)
	acct := &config.AccountConfig{
		RemoteHost: "127.0.0.1",
		RemotePort: addr.Port,
		RemoteTLS:  true,
	}

	conn, r, err := dialUpstream(acct, clientTLS)
	if err != nil {
		t.Fatalf("dialUpstream: %v", err)
	}
	conn.Close()

	if r == nil {
		t.Fatal("expected non-nil reader")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func TestDialUpstreamSTARTTLS(t *testing.T) {
	serverTLS, clientTLS := generateTestTLSConfigs(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	go func() {
		plain, err := ln.Accept()
		if err != nil {
			errCh <- fmt.Errorf("accept: %w", err)
			return
		}

		// Send initial plain greeting.
		fmt.Fprintf(plain, "* OK STARTTLS server ready\r\n")

		// Read STARTTLS command.
		pr := bufio.NewReader(plain)
		line, err := pr.ReadString('\n')
		if err != nil {
			plain.Close()
			errCh <- fmt.Errorf("read starttls cmd: %w", err)
			return
		}
		if !strings.Contains(line, "STARTTLS") {
			plain.Close()
			errCh <- fmt.Errorf("expected STARTTLS, got: %s", strings.TrimRight(line, "\r\n"))
			return
		}

		// Confirm STARTTLS.
		fmt.Fprintf(plain, "proxy0 OK begin TLS negotiation\r\n")

		// Upgrade to TLS.
		tlsConn := tls.Server(plain, serverTLS)
		if err := tlsConn.Handshake(); err != nil {
			tlsConn.Close()
			errCh <- fmt.Errorf("tls handshake: %w", err)
			return
		}

		// Send TLS greeting (read by the common greeting step in dialUpstream).
		fmt.Fprintf(tlsConn, "* OK TLS ready\r\n")
		tlsConn.Close()
		errCh <- nil
	}()

	addr := ln.Addr().(*net.TCPAddr)
	acct := &config.AccountConfig{
		RemoteHost:     "127.0.0.1",
		RemotePort:     addr.Port,
		RemoteStartTLS: true,
	}

	conn, r, err := dialUpstream(acct, clientTLS)
	if err != nil {
		t.Fatalf("dialUpstream: %v", err)
	}
	conn.Close()

	if r == nil {
		t.Fatal("expected non-nil reader")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func TestLoginUpstream(t *testing.T) {
	acct := &config.AccountConfig{
		RemoteUser:     "user@example.com",
		RemotePassword: `p@ss"word`, // contains a double-quote to test escaping
	}

	tests := []struct {
		name    string
		resp    string
		wantErr bool
	}{
		{
			name:    "success",
			resp:    "proxy0 OK LOGIN completed\r\n",
			wantErr: false,
		},
		{
			name:    "failure NO",
			resp:    "proxy0 NO LOGIN failed\r\n",
			wantErr: true,
		},
		{
			name:    "failure BAD",
			resp:    "proxy0 BAD command unknown\r\n",
			wantErr: true,
		},
		{
			name:    "success with untagged lines before",
			resp:    "* CAPABILITY IMAP4rev1\r\n* OK some note\r\nproxy0 OK LOGIN completed\r\n",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()

			// Server goroutine: read LOGIN command, send scripted response.
			go func() {
				defer serverConn.Close()
				r := bufio.NewReader(serverConn)
				// Read the LOGIN line.
				line, _ := r.ReadString('\n')
				// Verify quoting basics.
				if !strings.Contains(line, `"user@example.com"`) {
					// Still send response so client doesn't block.
					fmt.Fprint(serverConn, tt.resp)
					return
				}
				if !strings.Contains(line, `"p@ss\"word"`) {
					fmt.Fprint(serverConn, tt.resp)
					return
				}
				fmt.Fprint(serverConn, tt.resp)
			}()

			reader := bufio.NewReader(clientConn)
			err := LoginUpstream(clientConn, reader, acct)

			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoginUpstreamQuoting(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`simple`, `"simple"`},
		{`with"quote`, `"with\"quote"`},
		{`multiple""quotes`, `"multiple\"\"quotes"`},
		{``, `""`},
		{`with\backslash`, `"with\\backslash"`},
		{`back\and"quote`, `"back\\and\"quote"`},
		{`trailing\`, `"trailing\\"`},
	}

	for _, tt := range tests {
		got := quoteIMAPString(tt.input)
		if got != tt.want {
			t.Errorf("quoteIMAPString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
