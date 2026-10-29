// Package config persists provider settings and manages provider credentials.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Enabled bool            `json:"enabled"`
	Backend ProviderBackend `json:"backend"`
	BaseURL string          `json:"base-url"`
	Snap    *string         `json:"snap,omitempty"` // only applicable for Inference Snaps providers
	API     *string         `json:"api,omitempty"`
	KeyFile *string         `json:"key-file,omitempty"` // only applicable for OpenAI providers
}

// ConfigFilePath is expanded against the snap environment when read or written.
const (
	configFilePathEnvVar  = "INFERENCE_CONFIG_FILE"
	defaultConfigFilePath = "$SNAP_DATA/config.json"
)

// NewConfig enables provider discovery with an empty provider registry.
func newConfig() Config {
	c := Config{
		Version:           1,
		DiscoverProviders: true,
		Providers:         make(map[string]Provider),
	}
	return c
}

func configFilePath() string {
	customFilePath := os.Getenv(configFilePathEnvVar)
	if customFilePath != "" {
		return os.ExpandEnv(customFilePath)
	}
	return os.ExpandEnv(defaultConfigFilePath)
}

type ConfigReader struct {
	Path string
}

func NewConfigReader() *ConfigReader {
	return &ConfigReader{Path: configFilePath()}
}

func (c *ConfigReader) GetProviders() (map[string]Provider, error) {
	cfg, err := read(c.Path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return cfg.Providers, nil
}

// Read config content or default values if the file is absent.
func read(path string) (*Config, error) {
	path = os.ExpandEnv(path)

	// config file does not exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		config := newConfig()
		return &config, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return &config, nil
}

func write(path string, config *Config) error {
	path = os.ExpandEnv(path)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	data = append(data, '\n')
	if err := replaceFile(path, data); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

func replaceFile(path string, data []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() {
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("removing temporary config file: %w", removeErr))
		}
	}()

	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (c *Config) addProvider(name string, provider Provider) error {
	if name == "" {
		return fmt.Errorf("provider name cannot be empty")
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
func (c *Config) AddInferenceSnapProvider(name string, baseURL string, snap string, api *string) error {
	provider := Provider{
		Enabled: true,
		Backend: BackendSnap,
		BaseURL: baseURL,
		API:     api,
		Snap:    &snap,
		KeyFile: nil,
	}

	return c.addProvider(name, provider)
}

// AddOpenAIProvider registers an enabled proxy and persists its optional API key.
func (c *ConfigReader) AddOpenAIProvider(name string, baseURL string, apiKey *string) error {
	provider := Provider{
		Enabled: true,
		API:     new("openai"),
		Backend: BackendProxy,
		BaseURL: baseURL,
		Snap:    nil,
		KeyFile: nil,
	}

	cfg, err := read(c.Path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	err = cfg.addProvider(name, provider)
	if err != nil {
		return fmt.Errorf("adding provider: %w", err)
	}

	err = c.SetProviderKey(name, apiKey)
	if err != nil {
		return fmt.Errorf("setting provider key: %w", err)
	}

	err = write(c.Path, cfg)
	if err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

// RemoveProvider deletes a provider's credential file before removing its settings.
func (c *ConfigReader) RemoveProvider(name string) error {
	cfg, err := read(c.Path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	if cfg.Providers == nil {
		return nil
	}

	var provider Provider
	var exists bool
	if provider, exists = cfg.Providers[name]; !exists {
		return fmt.Errorf("provider %s does not exist", name)
	}

	if provider.KeyFile != nil {
		if err := os.Remove(*provider.KeyFile); err != nil {
			return fmt.Errorf("deleting key file: %v", err)
		}
	}

	delete(cfg.Providers, name)
	if err = write(c.Path, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// GetProviderKey reads stored credentials, returning nil if no key file is configured.
func (c *ConfigReader) GetProviderKey(providerName string) (*string, error) {
	cfg, err := read(c.Path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if cfg.Providers == nil {
		return nil, fmt.Errorf("provider %s does not exist", providerName)
	}

	provider, exists := cfg.Providers[providerName]
	if !exists {
		return nil, fmt.Errorf("provider %s does not exist", providerName)
	}

	if provider.KeyFile == nil {
		return nil, nil // no key configured
	}

	data, err := os.ReadFile(*provider.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}

	key := string(data)
	return &key, nil
}

// SetProviderKey persists credentials for a given provider, or removes them if the key is nil.
func (c *ConfigReader) SetProviderKey(providerName string, key *string) error {
	cfg, err := read(c.Path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	if cfg.Providers == nil {
		return fmt.Errorf("provider %s does not exist", providerName)
	}

	provider, exists := cfg.Providers[providerName]
	if !exists {
		return fmt.Errorf("provider %s does not exist", providerName)
	}

	if key == nil {
		// remove existing key (if any)
		if provider.KeyFile != nil {
			// we are removing an existing key
			if err := os.Remove(*provider.KeyFile); err != nil {
				return fmt.Errorf("deleting key file: %v", err)
			}
			provider.KeyFile = nil
		}
		cfg.Providers[providerName] = provider
		return write(c.Path, cfg)
	}

	keyFilePath := os.ExpandEnv(fmt.Sprintf("$SNAP_DATA/.keys/%s", providerName))

	if err := os.MkdirAll(filepath.Dir(keyFilePath), 0o700); err != nil {
		return fmt.Errorf("creating key file directory: %w", err)
	}

	err = os.WriteFile(keyFilePath, []byte(*key), 0o600)
	if err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}

	provider.KeyFile = &keyFilePath
	cfg.Providers[providerName] = provider
	return write(c.Path, cfg)
}
