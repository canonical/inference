// Package config loads and persists the user-facing inference configuration:
// proxy bind/port, external providers, and model aliases. It stores JSON under
// $SNAP_DATA (when running inside the snap) or the XDG config dir otherwise.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// Mutation is a declarative change to the config, brokered from the unprivileged
// CLI to the root daemon (which owns the writable store) over /v1/config.
type Mutation struct {
	Set      map[string]string `json:"set,omitempty"`      // dotted key -> value
	Provider *Provider         `json:"provider,omitempty"` // add or replace by name
}

// Apply mutates the config in place. It is the single place that knows valid
// keys, so the CLI and the daemon stay in agreement.
func (c *Config) Apply(m Mutation) error {
	for k, v := range m.Set {
		if err := c.Set(k, v); err != nil {
			return err
		}
	}
	if m.Provider != nil {
		c.AddProvider(*m.Provider)
	}
	return nil
}

// Set applies a single dotted key=value (proxy.port, proxy.bind, alias.<name>).
func (c *Config) Set(key, val string) error {
	switch key {
	case "proxy.port":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("proxy.port must be a number")
		}
		c.Proxy.Port = n
		return nil
	case "proxy.bind":
		c.Proxy.Bind = val
		return nil
	}
	if a, ok := strings.CutPrefix(key, "alias."); ok {
		if c.Aliases == nil {
			c.Aliases = map[string]string{}
		}
		c.Aliases[a] = val
		return nil
	}
	return fmt.Errorf("unknown key %q (try proxy.port, proxy.bind, alias.<name>)", key)
}

// Get reads a single dotted key. Provider API keys are deliberately not exposed.
func (c *Config) Get(key string) string {
	switch key {
	case "proxy.port":
		return strconv.Itoa(c.Proxy.Port)
	case "proxy.bind":
		return c.Proxy.Bind
	}
	if a, ok := strings.CutPrefix(key, "alias."); ok {
		return c.Aliases[a]
	}
	return ""
}

// AddProvider adds p, replacing any existing provider with the same name.
func (c *Config) AddProvider(p Provider) {
	out := c.Providers[:0]
	for _, ex := range c.Providers {
		if ex.Name != p.Name {
			out = append(out, ex)
		}
	}
	c.Providers = append(out, p)
}

// Redacted returns a shallow copy with provider API keys masked, for display.
func (c *Config) Redacted() *Config {
	cp := *c
	cp.Providers = make([]Provider, len(c.Providers))
	for i, p := range c.Providers {
		if p.APIKey != "" {
			p.APIKey = "***"
		}
		cp.Providers[i] = p
	}
	return &cp
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
