package config

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

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

	AllowedFolders  []string `toml:"allowed_folders"`
	BlockedFolders  []string `toml:"blocked_folders"`
	WritableFolders []string `toml:"writable_folders"`
}

// Load reads a TOML config file from path, validates it, and returns the Config.
func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("config: decode %s: %w", path, err)
	}

	seen := make(map[string]bool, len(cfg.Accounts))
	for i, acct := range cfg.Accounts {
		if seen[acct.LocalUser] {
			return nil, fmt.Errorf("config: duplicate local_user %q", acct.LocalUser)
		}
		seen[acct.LocalUser] = true

		if acct.RemoteTLS && acct.RemoteStartTLS {
			return nil, fmt.Errorf("config: account %q: remote_tls and remote_starttls cannot both be true", cfg.Accounts[i].LocalUser)
		}

		if len(acct.AllowedFolders) > 0 && len(acct.BlockedFolders) > 0 {
			return nil, fmt.Errorf("config: account %q: allowed_folders and blocked_folders cannot both be set", cfg.Accounts[i].LocalUser)
		}

		for _, wf := range acct.WritableFolders {
			if !acct.FolderAllowed(wf) {
				return nil, fmt.Errorf("config: account %q: writable folder %q is not allowed by folder filter", acct.LocalUser, wf)
			}
		}
	}

	return &cfg, nil
}

// HasFolderFilter reports whether the account has a folder allow or block list.
func (a *AccountConfig) HasFolderFilter() bool {
	return len(a.AllowedFolders) > 0 || len(a.BlockedFolders) > 0
}

// FolderAllowed reports whether the named folder is visible for this account.
func (a *AccountConfig) FolderAllowed(name string) bool {
	if len(a.AllowedFolders) > 0 {
		return matchesAny(name, a.AllowedFolders)
	}
	if len(a.BlockedFolders) > 0 {
		return !matchesAny(name, a.BlockedFolders)
	}
	return true
}

// FolderWritable reports whether the named folder is writable for this account.
func (a *AccountConfig) FolderWritable(name string) bool {
	return matchesAny(name, a.WritableFolders)
}

func matchesAny(name string, entries []string) bool {
	for _, entry := range entries {
		if folderMatch(name, entry) {
			return true
		}
	}
	return false
}

func folderMatch(name, pattern string) bool {
	n := normalizeINBOX(name)
	p := normalizeINBOX(pattern)
	if n == p {
		return true
	}
	return strings.HasPrefix(n, p+"/") || strings.HasPrefix(n, p+".")
}

// normalizeINBOX uppercases the INBOX prefix, since INBOX is
// case-insensitive in IMAP.
func normalizeINBOX(s string) string {
	if len(s) >= 5 && strings.EqualFold(s[:5], "INBOX") {
		if len(s) == 5 || s[5] == '/' || s[5] == '.' {
			return "INBOX" + s[5:]
		}
	}
	return s
}

// LookupUser returns the AccountConfig for the given username, or nil if not found.
func (c *Config) LookupUser(username string) *AccountConfig {
	for i := range c.Accounts {
		if c.Accounts[i].LocalUser == username {
			return &c.Accounts[i]
		}
	}
	return nil
}
