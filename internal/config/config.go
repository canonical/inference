// Package config persists provider settings and manages provider credentials.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ProviderAuthorizationScheme defines the type of authorization used by a provider.
type ProviderAuthorizationScheme string

// Supported provider authorization schemes.
const (
	AuthorizationNone   ProviderAuthorizationScheme = "none"
	AuthorizationAPIKey ProviderAuthorizationScheme = "api-key"
)

// ProviderBackend distinguishes local snaps from remote proxies.
type ProviderBackend string

// Supported provider backends.
const (
	BackendSnap  ProviderBackend = "snap"
	BackendProxy ProviderBackend = "proxy"
)

// Config contains versioned provider settings persisted as JSON.
type Config struct {
	Version           int                 `json:"version"`
	DiscoverProviders bool                `json:"discover-providers"`
	Providers         map[string]Provider `json:"providers"`
}

// Provider stores connection settings and optional snap or credential references.
type Provider struct {
	Enabled    bool                        `json:"enabled"`
	Backend    ProviderBackend             `json:"backend"`
	BaseURL    string                      `json:"base-url"`
	Snap       *string                     `json:"snap,omitempty"` // only applicable for Inference Snaps providers
	API        *string                     `json:"api,omitempty"`
	AuthScheme ProviderAuthorizationScheme `json:"auth-scheme"`
}

// ConfigFilePath is expanded against the snap environment when read or written.
const (
	configFilePathEnvVar  = "INFERENCE_CONFIG_FILE"
	defaultConfigFilePath = "$SNAP_DATA/config.json"
)

type ConfigReader struct {
	Path string
}

// NewConfigReader returns a ConfigReader initialized with the default config file path.
func NewConfigReader() *ConfigReader {
	return &ConfigReader{Path: configFilePath()}
}

func newConfig() Config {
	return Config{
		Version:           1,
		DiscoverProviders: true,
		Providers:         make(map[string]Provider),
	}
}

func configFilePath() string {
	customFilePath := os.Getenv(configFilePathEnvVar)
	if customFilePath != "" {
		return os.ExpandEnv(customFilePath)
	}
	return os.ExpandEnv(defaultConfigFilePath)
}

func validateProviderName(name string) error {
	if name == "" {
		return errors.New("provider name cannot be empty")
	}
	if name == "." || strings.Contains(name, "..") || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("invalid provider name %q", name)
	}
	return nil
}

func (c *ConfigReader) GetProviders() (map[string]Provider, error) {
	cfg, err := readConfig(c.Path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return cfg.Providers, nil
}

func (c *ConfigReader) ProviderExists(name string) (bool, error) {
	providers, err := c.GetProviders()
	if err != nil {
		return false, err
	}
	_, exists := providers[name]
	return exists, nil
}

func (c *Config) addProvider(name string, provider Provider) error {
	if err := validateProviderName(name); err != nil {
		return err
	}

	if c.Providers == nil {
		c.Providers = make(map[string]Provider)
	}

	if _, exists := c.Providers[name]; exists {
		return fmt.Errorf("provider %s already exists", name)
	}

	c.Providers[name] = provider
	return nil
}

// AddInferenceSnapProvider registers an enabled snap, rejecting empty or duplicate names.
func (c *ConfigReader) AddInferenceSnapProvider(name string, baseURL string, snap string, api *string) error {
	cfg, unlock, err := readConfigLocked(c.Path)
	if err != nil {
		return err
	}
	defer unlock()
	provider := Provider{
		Enabled:    true,
		Backend:    BackendSnap,
		BaseURL:    baseURL,
		API:        api,
		Snap:       &snap,
		AuthScheme: AuthorizationNone,
	}

	err = cfg.addProvider(name, provider)
	if err != nil {
		return fmt.Errorf("adding provider: %w", err)
	}
	return writeConfig(c.Path, cfg)
}

// AddOpenAIProvider registers an enabled proxy and persists its optional API key.
func (c *ConfigReader) AddOpenAIProvider(name string, baseURL string, apiKey *string) error {
	cfg, unlock, err := readConfigLocked(c.Path)
	if err != nil {
		return err
	}
	defer unlock()

	provider := Provider{
		Enabled:    true,
		API:        new("openai"),
		Backend:    BackendProxy,
		BaseURL:    baseURL,
		Snap:       nil,
		AuthScheme: AuthorizationNone,
	}

	err = cfg.addProvider(name, provider)
	if err != nil {
		return fmt.Errorf("adding provider: %w", err)
	}
	keyFilePath, err := getProviderKeyFilePath(c.Path, name)
	if err != nil {
		return err
	}
	previousKey, err := snapshotCredential(keyFilePath)
	if err != nil {
		return fmt.Errorf("reading key file: %w", err)
	}

	err = cfg.setProviderKey(name, apiKey, keyFilePath)
	if err != nil {
		return errors.Join(fmt.Errorf("setting provider key: %w", err), previousKey.restore(keyFilePath))
	}

	err = writeConfig(c.Path, cfg)
	if err != nil {
		return errors.Join(fmt.Errorf("writing config: %w", err), previousKey.restore(keyFilePath))
	}

	return nil
}

// RemoveProvider removes a provider's settings, then deletes its credential file.
func (c *ConfigReader) RemoveProvider(name string) error {
	cfg, unlock, err := readConfigLocked(c.Path)
	if err != nil {
		return err
	}
	defer unlock()

	provider, exists := cfg.Providers[name]
	if !exists {
		return fmt.Errorf("provider %s does not exist", name)
	}

	if provider.AuthScheme != AuthorizationNone && provider.AuthScheme != AuthorizationAPIKey {
		return fmt.Errorf("unsupported authorization scheme: %v", provider.AuthScheme)
	}
	keyFilePath, err := getProviderKeyFilePath(c.Path, name)
	if err != nil {
		return err
	}

	delete(cfg.Providers, name)
	if err := writeConfig(c.Path, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Remove(keyFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("deleting key file: %w", err)
	}
	return nil
}
