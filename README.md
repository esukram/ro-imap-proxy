# ro-imap-proxy

A read-only IMAP proxy that maps local credentials to upstream IMAP servers. All mutating commands are blocked at the wire protocol level, and `SELECT` is rewritten to `EXAMINE` so mailboxes are always opened read-only.

## How it works

The proxy sits between an IMAP client and a remote IMAP server. It accepts plaintext IMAP connections, authenticates clients against a local TOML config, then connects to the configured upstream server over TLS or STARTTLS.

After authentication, two goroutines handle bidirectional traffic:
- **Client → Upstream**: parses each command, applies the read-only filter (allow/block/rewrite), and forwards or rejects.
- **Upstream → Client**: forwards all server responses verbatim.

### Blocked commands

STORE, COPY, MOVE, DELETE, EXPUNGE, APPEND, CREATE, RENAME, SUBSCRIBE, UNSUBSCRIBE, AUTHENTICATE

UID subcommands: UID STORE, UID COPY, UID MOVE, UID EXPUNGE

### Rewritten commands

`SELECT` → `EXAMINE` (opens mailbox read-only)

### Supported features

- IMAP IDLE
- IMAP LITERAL and LITERAL+ (synchronizing and non-synchronizing literals)
- TLS and STARTTLS upstream connections
- Multiple accounts with independent upstream servers

## Building

```
go build ./cmd/ro-imap-proxy/
```

## Configuration

Copy `config.example.toml` to `config.toml` and edit:

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
# remote_starttls = true  # mutually exclusive with remote_tls
```

Multiple `[[accounts]]` sections can be defined. Each maps a local username/password pair to a remote IMAP server with its own credentials.

Validation rules:
- `local_user` must be unique across all accounts
- `remote_tls` and `remote_starttls` cannot both be `true`

## Usage

```
./ro-imap-proxy -config config.toml
```

The `-config` flag defaults to `config.toml` in the current directory.

Logs are written to stderr using `log/slog`. Send SIGINT or SIGTERM for graceful shutdown.

## Testing

```
go test ./...
```
