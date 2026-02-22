package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.toml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestLoad(t *testing.T) {
	validTOML := `
[server]
listen = ":143"

[[accounts]]
local_user = "reader1"
local_password = "pass1"
remote_host = "mail.example.com"
remote_port = 993
remote_user = "user1@example.com"
remote_password = "rempass1"
remote_tls = true

[[accounts]]
local_user = "reader2"
local_password = "pass2"
remote_host = "mail.example.com"
remote_port = 143
remote_user = "user2@example.com"
remote_password = "rempass2"
remote_starttls = true
`

	tests := []struct {
		name    string
		content string
		path    string // if set, use this path instead of temp file
		wantErr bool
		check   func(t *testing.T, cfg *Config)
	}{
		{
			name:    "valid config",
			content: validTOML,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Server.Listen != ":143" {
					t.Errorf("listen = %q, want %q", cfg.Server.Listen, ":143")
				}
				if len(cfg.Accounts) != 2 {
					t.Fatalf("len(accounts) = %d, want 2", len(cfg.Accounts))
				}
				a := cfg.Accounts[0]
				if a.LocalUser != "reader1" {
					t.Errorf("accounts[0].local_user = %q, want %q", a.LocalUser, "reader1")
				}
				if !a.RemoteTLS {
					t.Error("accounts[0].remote_tls should be true")
				}
				if a.RemoteStartTLS {
					t.Error("accounts[0].remote_starttls should be false")
				}
			},
		},
		{
			name:    "file not found",
			path:    filepath.Join(t.TempDir(), "nonexistent.toml"),
			wantErr: true,
		},
		{
			name:    "invalid TOML syntax",
			content: `[server\nlisten = this is not valid toml!!!`,
			wantErr: true,
		},
		{
			name: "duplicate local_user",
			content: `
[server]
listen = ":143"

[[accounts]]
local_user = "dup"
local_password = "p1"
remote_host = "h"
remote_port = 993
remote_user = "u1"
remote_password = "rp1"
remote_tls = true

[[accounts]]
local_user = "dup"
local_password = "p2"
remote_host = "h"
remote_port = 993
remote_user = "u2"
remote_password = "rp2"
remote_tls = true
`,
			wantErr: true,
		},
		{
			name: "conflicting TLS flags",
			content: `
[server]
listen = ":143"

[[accounts]]
local_user = "u1"
local_password = "p1"
remote_host = "h"
remote_port = 143
remote_user = "ru"
remote_password = "rp"
remote_tls = true
remote_starttls = true
`,
			wantErr: true,
		},
		{
			name: "conflicting folder lists",
			content: `
[server]
listen = ":143"

[[accounts]]
local_user = "u1"
local_password = "p1"
remote_host = "h"
remote_port = 143
remote_user = "ru"
remote_password = "rp"
allowed_folders = ["INBOX"]
blocked_folders = ["Trash"]
`,
			wantErr: true,
		},
		{
			name: "writable folder in block list",
			content: `
[server]
listen = ":143"

[[accounts]]
local_user = "u1"
local_password = "p1"
remote_host = "h"
remote_port = 143
remote_user = "ru"
remote_password = "rp"
blocked_folders = ["Drafts"]
writable_folders = ["Drafts"]
`,
			wantErr: true,
		},
		{
			name: "writable folder not in allow list",
			content: `
[server]
listen = ":143"

[[accounts]]
local_user = "u1"
local_password = "p1"
remote_host = "h"
remote_port = 143
remote_user = "ru"
remote_password = "rp"
allowed_folders = ["INBOX", "Sent"]
writable_folders = ["Drafts"]
`,
			wantErr: true,
		},
		{
			name: "writable folder in allow list",
			content: `
[server]
listen = ":143"

[[accounts]]
local_user = "u1"
local_password = "p1"
remote_host = "h"
remote_port = 143
remote_user = "ru"
remote_password = "rp"
allowed_folders = ["INBOX", "Sent", "Drafts"]
writable_folders = ["Drafts"]
`,
			check: func(t *testing.T, cfg *Config) {
				if !cfg.Accounts[0].FolderWritable("Drafts") {
					t.Error("expected Drafts to be writable")
				}
			},
		},
		{
			name: "writable folder no folder filter",
			content: `
[server]
listen = ":143"

[[accounts]]
local_user = "u1"
local_password = "p1"
remote_host = "h"
remote_port = 143
remote_user = "ru"
remote_password = "rp"
writable_folders = ["Drafts"]
`,
			check: func(t *testing.T, cfg *Config) {
				if !cfg.Accounts[0].FolderWritable("Drafts") {
					t.Error("expected Drafts to be writable")
				}
			},
		},
		{
			name: "no TLS flags both false is valid",
			content: `
[server]
listen = ":143"

[[accounts]]
local_user = "u1"
local_password = "p1"
remote_host = "h"
remote_port = 143
remote_user = "ru"
remote_password = "rp"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Accounts[0].RemoteTLS || cfg.Accounts[0].RemoteStartTLS {
					t.Error("expected both TLS flags false")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if path == "" {
				path = writeTemp(t, tt.content)
			}

			cfg, err := Load(path)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestLookupUser(t *testing.T) {
	cfg := &Config{
		Accounts: []AccountConfig{
			{LocalUser: "alice", LocalPassword: "apass", RemoteHost: "h1", RemotePort: 993, RemoteTLS: true},
			{LocalUser: "bob", LocalPassword: "bpass", RemoteHost: "h2", RemotePort: 143, RemoteStartTLS: true},
		},
	}

	tests := []struct {
		username  string
		wantNil   bool
		wantUser  string
	}{
		{"alice", false, "alice"},
		{"bob", false, "bob"},
		{"charlie", true, ""},
		{"", true, ""},
		{"Alice", true, ""}, // case-sensitive
	}

	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			got := cfg.LookupUser(tt.username)
			if tt.wantNil {
				if got != nil {
					t.Errorf("LookupUser(%q) = %v, want nil", tt.username, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("LookupUser(%q) = nil, want non-nil", tt.username)
			}
			if got.LocalUser != tt.wantUser {
				t.Errorf("LookupUser(%q).LocalUser = %q, want %q", tt.username, got.LocalUser, tt.wantUser)
			}
		})
	}
}

func TestHasFolderFilter(t *testing.T) {
	tests := []struct {
		name string
		acct AccountConfig
		want bool
	}{
		{"no filter", AccountConfig{}, false},
		{"allow list", AccountConfig{AllowedFolders: []string{"INBOX"}}, true},
		{"block list", AccountConfig{BlockedFolders: []string{"Trash"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.acct.HasFolderFilter(); got != tt.want {
				t.Errorf("HasFolderFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFolderAllowed(t *testing.T) {
	tests := []struct {
		name   string
		acct   AccountConfig
		folder string
		want   bool
	}{
		// Allow-list tests.
		{"allow exact match", AccountConfig{AllowedFolders: []string{"INBOX", "Sent"}}, "INBOX", true},
		{"allow no match", AccountConfig{AllowedFolders: []string{"INBOX", "Sent"}}, "Trash", false},
		{"allow child match slash", AccountConfig{AllowedFolders: []string{"Archive"}}, "Archive/2024", true},
		{"allow child match dot", AccountConfig{AllowedFolders: []string{"Archive"}}, "Archive.2024", true},
		{"allow parent not matched by child entry", AccountConfig{AllowedFolders: []string{"Archive/2024"}}, "Archive", false},

		// Block-list tests.
		{"block exact match", AccountConfig{BlockedFolders: []string{"Spam", "Trash"}}, "Spam", false},
		{"block no match allowed", AccountConfig{BlockedFolders: []string{"Spam", "Trash"}}, "INBOX", true},
		{"block child match", AccountConfig{BlockedFolders: []string{"Trash"}}, "Trash/Subfolder", false},

		// INBOX case insensitivity.
		{"inbox case insensitive allow", AccountConfig{AllowedFolders: []string{"inbox"}}, "INBOX", true},
		{"inbox case insensitive block", AccountConfig{BlockedFolders: []string{"inbox"}}, "INBOX", false},
		{"inbox case insensitive name", AccountConfig{AllowedFolders: []string{"INBOX"}}, "inbox", true},

		// No filter.
		{"no filter", AccountConfig{}, "Anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.acct.FolderAllowed(tt.folder)
			if got != tt.want {
				t.Errorf("FolderAllowed(%q) = %v, want %v", tt.folder, got, tt.want)
			}
		})
	}
}

func TestFolderWritable(t *testing.T) {
	tests := []struct {
		name   string
		acct   AccountConfig
		folder string
		want   bool
	}{
		{"no writable folders", AccountConfig{}, "INBOX", false},
		{"exact match", AccountConfig{WritableFolders: []string{"Drafts"}}, "Drafts", true},
		{"no match", AccountConfig{WritableFolders: []string{"Drafts"}}, "INBOX", false},
		{"child match", AccountConfig{WritableFolders: []string{"Drafts"}}, "Drafts/Sub", true},
		{"INBOX normalization", AccountConfig{WritableFolders: []string{"inbox"}}, "INBOX", true},
		{"empty string", AccountConfig{WritableFolders: []string{"Drafts"}}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.acct.FolderWritable(tt.folder)
			if got != tt.want {
				t.Errorf("FolderWritable(%q) = %v, want %v", tt.folder, got, tt.want)
			}
		})
	}
}

func TestLookupUserReturnPointer(t *testing.T) {
	// Verify that the returned pointer is to the slice element, not a copy
	cfg := &Config{
		Accounts: []AccountConfig{
			{LocalUser: "alice", LocalPassword: "secret"},
		},
	}
	got := cfg.LookupUser("alice")
	if got == nil {
		t.Fatal("LookupUser returned nil")
	}
	// Modifying through pointer should affect config
	got.LocalPassword = "changed"
	if cfg.Accounts[0].LocalPassword != "changed" {
		t.Error("LookupUser did not return pointer to slice element")
	}
}
