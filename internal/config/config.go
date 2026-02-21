package config

import (
	"fmt"

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
	}

	return &cfg, nil
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
