# CLAUDE.md

## Build & test

```
go build ./cmd/ro-imap-proxy/
go test ./...
go vet ./...
```

Run a single package's tests: `go test ./internal/proxy/ -v -count=1`

## Project structure

```
cmd/ro-imap-proxy/main.go     Entry point, flags, signal handling
internal/
  config/                      TOML config loading and account lookup
  imap/                        IMAP command parsing, literal detection, read-only filter
  proxy/                       Upstream dialing, session lifecycle, TCP server
config.example.toml            Example configuration
```

## Architecture

Raw TCP line-based proxy — no IMAP library. Parses only tag + command verb from each client line. Server responses pass through verbatim.

- Pre-auth: CAPABILITY, NOOP, LOGOUT handled locally. LOGIN looks up config, dials upstream with TLS/STARTTLS, authenticates with remote credentials.
- Post-auth: two goroutines (client→upstream filtered, upstream→client verbatim). Cleanup via `sync.Once`.
- SELECT is rewritten to EXAMINE (positional replacement in raw line).
- IDLE is handled by forwarding to upstream, relying on the upstream→client goroutine for the `+` continuation and untagged responses, then waiting for DONE from client.
- Literals ({N} sync, {N+} non-sync) are forwarded byte-for-byte. For blocked commands with non-sync literals, the literal data is consumed and discarded.
- LOGOUT in post-auth is handled locally (not forwarded to upstream) to ensure clean connection teardown.

## Dependencies

- `github.com/BurntSushi/toml` for config parsing
- stdlib only otherwise (`crypto/tls`, `log/slog`, `net`, `bufio`, `sync`)

## Code conventions

- stdlib `testing` only, no testify
- Table-driven tests
- Tests use `net.Pipe()` with injected `dialUpstream` for fake upstream simulation
- Fake upstreams do NOT send a greeting (the injected dialer replaces `DialUpstream` which would have consumed it)
