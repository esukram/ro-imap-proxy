# imap-proxy

An IMAP proxy that maps local credentials to upstream IMAP servers. By default all mailboxes are read-only — mutating commands are blocked and `SELECT` is rewritten to `EXAMINE`. Per-account writable folders can be configured to allow drafts and flag changes where needed.

## How it works

The proxy sits between an IMAP client and a remote IMAP server. It accepts plaintext IMAP connections, authenticates clients against a local TOML config, then connects to the configured upstream server over TLS or STARTTLS.

After authentication, two goroutines handle bidirectional traffic:
- **Client → Upstream**: parses each command, applies the read-only filter (allow/block/rewrite), and forwards or rejects.
- **Upstream → Client**: forwards all server responses verbatim.

### Default read-only behavior

By default, all mutating commands are blocked:

STORE, COPY, MOVE, DELETE, EXPUNGE, APPEND, CREATE, RENAME, SUBSCRIBE, UNSUBSCRIBE, AUTHENTICATE

UID subcommands: UID STORE, UID COPY, UID MOVE, UID EXPUNGE

`SELECT` is rewritten to `EXAMINE` (opens mailbox read-only).

### Writable folders

Per-account `writable_folders` can be configured to selectively allow writes. For writable folders:

- **SELECT** passes through as-is (not rewritten to EXAMINE)
- **STORE** and **UID STORE** are allowed (e.g. flag changes)
- **APPEND** is allowed (e.g. saving drafts)

All other mutating commands (COPY, MOVE, DELETE, EXPUNGE, CREATE, RENAME, etc.) remain blocked even in writable folders.

### Supported features

- IMAP IDLE
- IMAP LITERAL and LITERAL+ (synchronizing and non-synchronizing literals)
- TLS and STARTTLS upstream connections
- Multiple accounts with independent upstream servers
- Per-account folder allow/block lists
- Per-account writable folders

## Building

```
go build ./cmd/imap-proxy/
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

# writable_folders = ["Drafts"]
```

Multiple `[[accounts]]` sections can be defined. Each maps a local username/password pair to a remote IMAP server with its own credentials.

Validation rules:
- `local_user` must be unique across all accounts
- `remote_tls` and `remote_starttls` cannot both be `true`
- `allowed_folders` and `blocked_folders` cannot both be set
- `writable_folders` entries must pass the folder allow/block filter

## Usage

```
./imap-proxy -config config.toml
```

The `-config` flag defaults to `config.toml` in the current directory.

Logs are written to stderr using `log/slog`. Send SIGINT or SIGTERM for graceful shutdown.

## Testing

```
go test ./...
```
