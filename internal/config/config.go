// Package config loads and persists the user-facing inference configuration:
// proxy bind/port, external providers, and model aliases. It stores JSON under
// $SNAP_DATA (when running inside the snap) or the XDG config dir otherwise.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ProxyCfg controls the federated OpenAI-compatible gateway.
type ProxyCfg struct {
	Bind string `json:"bind"`
	Port int    `json:"port"`
}

// Provider is an external (remote) OpenAI-compatible backend, e.g. Anthropic
// or OpenAI, registered via `inference proxy add`.
type Provider struct {
	Name    string   `json:"name"`              // logical name, e.g. "anthropic"
	Type    string   `json:"type"`              // openai | anthropic
	BaseURL string   `json:"base_url"`          // OpenAI-compatible base, incl. /v1
	APIKey  string   `json:"api_key,omitempty"` // bearer key
	Models  []string `json:"models,omitempty"`  // advertised model ids
}

// Config is the persisted document.
type Config struct {
	Proxy     ProxyCfg          `json:"proxy"`
	Providers []Provider        `json:"providers,omitempty"`
	Aliases   map[string]string `json:"aliases,omitempty"`
}

// Dir returns the directory holding config.json.
func Dir() string {
	if d := os.Getenv("SNAP_DATA"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "inference")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "inference")
}

// Path returns the full path to config.json.
func Path() string { return filepath.Join(Dir(), "config.json") }

func defaults() *Config {
	return &Config{
		Proxy:   ProxyCfg{Bind: "127.0.0.1", Port: 8080},
		Aliases: map[string]string{},
	}
}

// Load reads config.json, returning sensible defaults if it does not exist.
func Load() (*Config, error) {
	c := defaults()
	b, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return c, err
	}
	if c.Aliases == nil {
		c.Aliases = map[string]string{}
	}
	if c.Proxy.Port == 0 {
		c.Proxy.Port = 8080
	}
	if c.Proxy.Bind == "" {
		c.Proxy.Bind = "127.0.0.1"
	}
	return c, nil
}

// Save writes config.json, creating the directory if needed.
func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), b, 0o600)
}
