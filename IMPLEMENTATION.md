# Implementation Guide: imap-proxy

## Overview

A Go-based read-only IMAP proxy that maps local credentials (from a TOML config) to upstream IMAP server credentials. Exposes mailboxes in read-only mode by blocking all mutating IMAP commands at the wire protocol level.

## Architecture

**Raw TCP line-based proxy** — no IMAP library. The proxy reads IMAP lines, parses only the tag + command verb, decides allow/block/rewrite, and forwards or rejects. Server responses pass through verbatim.

```
Client (plaintext, port 143)
  │
  ▼
┌──────────────────────────────┐
│ imap-proxy                │
│                              │
│ Pre-auth:                    │
│   CAPABILITY/NOOP → local    │
│   LOGIN → lookup config,     │
│     dial upstream, auth      │
│                              │
│ Post-auth (2 goroutines):    │
│   Client→Upstream:           │
│     parse cmd, filter,       │
│     forward or reject        │
│   Upstream→Client:           │
│     forward verbatim         │
└──────────────────────────────┘
  │
  ▼
Remote IMAP Server (TLS/STARTTLS, port 993/143)
```

## Project Structure

```
cmd/imap-proxy/main.go          — entry point: flags, config load, start server
internal/
  config/
    config.go                      — Config, ServerConfig, AccountConfig types; Load(); LookupUser()
    config_test.go
  imap/
    command.go                     — Command type (Tag, Verb, SubVerb, Raw); ParseCommand()
    command_test.go
    literal.go                     — ParseLiteral(): detect {N} or {N+} at end of line
    literal_test.go
    filter.go                      — Action type (Allow/Block/Rewrite); FilterResult; Filter()
    filter_test.go
  proxy/
    upstream.go                    — DialUpstream() (TLS/STARTTLS); LoginUpstream()
    upstream_test.go
    session.go                     — Session: pre-auth loop, post-auth bidirectional pipe, IDLE, literals
    session_test.go
    server.go                      — Server: TCP listener, accept loop, spawn sessions
    server_test.go
config.example.toml
README.md
CLAUDE.md
```

## Dependencies

- `github.com/BurntSushi/toml` — TOML config parsing
- `log/slog` (stdlib) — structured logging
- `crypto/tls` (stdlib) — upstream TLS/STARTTLS

## Config Format (TOML)

```toml
[server]
listen = ":143"

[[accounts]]
local_user = "reader1"
local_password = "localpass1"
remote_host = "mail.example.com"
remote_port = 993
remote_user = "realuser@example.com"
remote_password = "realpass"
remote_tls = true
# remote_starttls = true  # mutually exclusive with remote_tls; validate at load time
```

## Key Types

### config.Config

```go
type Config struct {
    Server   ServerConfig    `toml:"server"`
    Accounts []AccountConfig `toml:"accounts"`
}
type ServerConfig struct {
    Listen string `toml:"listen"`
}
type AccountConfig struct {
    LocalUser      string `toml:"local_user"`
    LocalPassword  string `toml:"local_password"`
    RemoteHost     string `toml:"remote_host"`
    RemotePort     int    `toml:"remote_port"`
    RemoteUser     string `toml:"remote_user"`
    RemotePassword string `toml:"remote_password"`
    RemoteTLS      bool   `toml:"remote_tls"`
    RemoteStartTLS bool   `toml:"remote_starttls"`
}
```

`Load(path)` reads the file, validates no duplicate `local_user`, validates that `remote_tls` and `remote_starttls` aren't both true.

`LookupUser(username)` returns the matching `*AccountConfig` or nil.

### imap.Command

```go
type Command struct {
    Tag     string   // e.g. "A001"
    Verb    string   // uppercased, e.g. "SELECT", "UID"
    SubVerb string   // for UID commands: "FETCH", "STORE", etc.
    Raw     []byte   // original line including CRLF
}
```

`ParseCommand(line []byte) (Command, error)` — find first SP → tag, find next SP or CRLF → verb, if verb is "UID" extract subverb. Uppercase verb and subverb.

### imap.ParseLiteral

`ParseLiteral(line []byte) (n int64, nonSync bool, ok bool)` — scan backwards from CRLF for `}`, then `{`, parse integer between them. Check for `+` before `}` for LITERAL+ (non-synchronizing).

### imap.Filter

```go
type Action int
const ( Allow Action = iota; Block; Rewrite )

type FilterResult struct {
    Action    Action
    Rewritten []byte   // only if Rewrite
    RejectMsg string   // only if Block
}
```

`Filter(cmd Command) FilterResult`

**Blocked verbs**: STORE, COPY, MOVE, DELETE, EXPUNGE, APPEND, CREATE, RENAME, SUBSCRIBE, UNSUBSCRIBE, AUTHENTICATE

**Blocked UID subverbs**: STORE, COPY, MOVE, EXPUNGE

**Rewrite**: SELECT → EXAMINE (replace verb in raw line, keep tag and args)

**Allow**: everything else

### proxy.Session

```go
type SessionState int
const (
    StateGreeting SessionState = iota
    StateNotAuth
    StateAuth
    StateSelected
    StateIdle
)

type Session struct {
    clientConn   net.Conn
    upstreamConn net.Conn
    clientR      *bufio.Reader
    upstreamR    *bufio.Reader
    state        SessionState
    account      *config.AccountConfig
    logger       *slog.Logger
}
```

### proxy.Server

```go
type Server struct {
    config   *config.Config
    listener net.Listener
    logger   *slog.Logger
}
```

`NewServer(cfg, logger)`, `ListenAndServe()`, `Close()`

## Session Lifecycle

1. **Greeting**: send `* OK imap-proxy ready\r\n`
2. **Pre-auth loop**: read client commands, handle locally:
   - `CAPABILITY` → respond with hardcoded list (IMAP4rev1, IDLE, etc.; no STARTTLS advertised)
   - `NOOP` → `tag OK`
   - `LOGOUT` → `* BYE` + `tag OK`, close
   - `LOGIN user pass` → look up user in config, verify local password, dial upstream (TLS/STARTTLS), send LOGIN with remote credentials. On success: `tag OK`. On failure: `tag NO`.
   - Anything else → `tag BAD`
3. **Post-auth**: spawn two goroutines:
   - **Client→Upstream**: read line, `ParseCommand`, `Filter`, forward/reject. Handle literals and IDLE state.
   - **Upstream→Client**: forward bytes verbatim. No filtering needed.
4. **Teardown**: on error in either goroutine, close both connections. Use `sync.Once` for cleanup.

## Authentication Flow

1. Client: `A001 LOGIN reader1 localpass1`
2. Proxy: parse username + password from LOGIN args (handle quoted strings and literals)
3. Proxy: `config.LookupUser("reader1")` → find AccountConfig
4. Proxy: verify `localpass1 == account.LocalPassword`
5. Proxy: `DialUpstream(account)` — connect with TLS or STARTTLS
6. Proxy: read and discard upstream greeting
7. Proxy: `LoginUpstream(conn, reader, account)` — send `proxy0 LOGIN "remoteuser" "remotepass"`
8. Success → `A001 OK LOGIN completed` to client
9. Failure → `A001 NO LOGIN failed`, close upstream

## SELECT → EXAMINE Rewrite

Replace the verb in the raw line: `A002 SELECT INBOX\r\n` → `A002 EXAMINE INBOX\r\n`. Client sees `[READ-ONLY]` in the response.

## IDLE Handling

1. Forward `tag IDLE\r\n` to upstream
2. Read `+ continuation` from upstream, forward to client
3. Client→Upstream goroutine blocks waiting for `DONE\r\n` from client
4. Upstream→Client goroutine continues forwarding untagged responses (`* N EXISTS`, etc.)
5. Client sends `DONE\r\n` → forward to upstream
6. Upstream sends `tag OK IDLE terminated` → forwarded by upstream→client goroutine
7. Resume normal command loop

## Literal Handling

When a client line ends with `{N}\r\n`:
- **Synchronizing** (`{N}\r\n`): forward line to upstream, wait for `+` from upstream, forward `+` to client, then copy exactly N bytes from client to upstream
- **Non-synchronizing** (`{N+}\r\n`): forward line, immediately copy N bytes
- After copying, read next line (may be continuation of same command, or another literal)

For blocked commands (e.g., APPEND with a literal): reject at the first line. For synchronizing literals, the client hasn't sent the data yet so nothing extra to consume. For non-synchronizing literals, the client may have already sent the data — need to consume and discard N bytes.

## Upstream Connection (upstream.go)

`DialUpstream(acct *AccountConfig) (net.Conn, string, error)`:
- If `remote_tls`: `tls.Dial("tcp", host:port, &tls.Config{ServerName: host})`
- If `remote_starttls`: `net.Dial("tcp", host:port)`, read greeting, send `tag STARTTLS`, read OK, upgrade with `tls.Client(conn, tlsConfig)`
- Read and return the greeting line

`LoginUpstream(conn, reader, acct) error`:
- Send `proxy0 LOGIN "user" "pass"` (properly quote/escape)
- Read tagged response, check for OK

## Logging

- `log/slog` with `slog.NewTextHandler(os.Stderr, ...)`
- Per-session logger: `slog.With("client", remoteAddr, "user", username)`
- Info: connections, logins, disconnects
- Warn: blocked commands
- Error: upstream failures, protocol errors
- Debug: every forwarded command

## Testing Strategy

**Unit tests** (table-driven):
- `command_test.go`: parse various command formats, edge cases
- `literal_test.go`: detect literals, non-sync literals, no-literal lines
- `filter_test.go`: every blocked/allowed/rewritten command
- `config_test.go`: valid/invalid TOML, duplicate users, conflicting TLS flags

**Integration tests** (using `net.Pipe()` or localhost TCP):
- `session_test.go`: simulate client+fake-upstream conversations as `{send, expect}` pairs
- `server_test.go`: full TCP accept loop test
- `upstream_test.go`: dial fake TLS/STARTTLS servers

## Implementation Order

1. `go.mod` init + `go get github.com/BurntSushi/toml`
2. `internal/config/` — types, Load(), LookupUser(), tests
3. `internal/imap/command.go` + `literal.go` — parsing, tests
4. `internal/imap/filter.go` — filter logic, tests
5. `internal/proxy/upstream.go` — dial + login, tests
6. `internal/proxy/session.go` — core session logic (biggest piece, ~300 lines)
7. `internal/proxy/server.go` — TCP accept loop
8. `cmd/imap-proxy/main.go` — wire everything
9. `config.example.toml`
10. `README.md`
11. `CLAUDE.md`

## Entry Point (main.go)

```go
func main() {
    configPath := flag.String("config", "config.toml", "path to config file")
    flag.Parse()
    logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
    cfg, err := config.Load(*configPath)
    // ... error handling, create server, ListenAndServe
}
```
