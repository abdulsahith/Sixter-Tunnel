// Package config reads/writes ~/.sixter/config.yaml — the credentials file
// created by "sixter login" and consumed by the tunnel/repo clients.
//
// The file is a flat YAML document, parsed with a tiny line parser so we
// don't pull in a YAML dependency.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk credential set for one Sixter account.
type Config struct {
	Email     string // account email
	Token     string // raw API token (keep this file private!)
	TunnelSub string // subdomain for tunnels, e.g. "sahith"
	RepoSub   string // subdomain for the repo dashboard, e.g. "sahith-repo"
	Server    string // tunnel server ws url, e.g. "ws://sixter.in:8008"
	API       string // api base url, e.g. "https://sixter.in"
}

// Dir returns the config directory (~/.sixter), creating nothing.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sixter"), nil
}

// Path returns the full path of config.yaml.
func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.yaml"), nil
}

// Load reads the config file. Returns (nil, nil) if it doesn't exist.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	c := &Config{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)

		switch key {
		case "email":
			c.Email = val
		case "token":
			c.Token = val
		case "tunnel_subdomain":
			c.TunnelSub = val
		case "repo_subdomain":
			c.RepoSub = val
		case "server":
			// "server: ws://host:port" — the value itself contains ":",
			// which is why we split on the FIRST ":" above.
			c.Server = val
		case "api":
			c.API = val
		}
	}
	return c, nil
}

// Save writes the config file with owner-only permissions.
func (c *Config) Save() error {
	d, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0700); err != nil {
		return err
	}
	p := filepath.Join(d, "config.yaml")

	content := fmt.Sprintf(`# Sixter credentials — created by "sixter login". Keep this file private.
email: %s
token: %s
tunnel_subdomain: %s
repo_subdomain: %s
server: %s
api: %s
`, c.Email, c.Token, c.TunnelSub, c.RepoSub, c.Server, c.API)

	return os.WriteFile(p, []byte(content), 0600)
}
