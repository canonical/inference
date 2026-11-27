// Package config persists provider settings.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Providers map[string]Provider `json:"providers"`
}

type Provider struct {
	Enabled bool `json:"enabled"`
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
		Providers: make(map[string]Provider),
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

func (c *ConfigReader) AddProvider(name string, enabled bool) error {
	cfg, unlock, err := readConfigLocked(c.Path)
	if err != nil {
		return err
	}
	defer unlock()

	if err := cfg.addProvider(name, Provider{Enabled: enabled}); err != nil {
		return fmt.Errorf("adding provider: %w", err)
	}
	return writeConfig(c.Path, cfg)
}

func (c *ConfigReader) RemoveProvider(name string) error {
	cfg, unlock, err := readConfigLocked(c.Path)
	if err != nil {
		return err
	}
	defer unlock()

	if _, exists := cfg.Providers[name]; !exists {
		return fmt.Errorf("provider %s does not exist", name)
	}
	delete(cfg.Providers, name)
	if err := writeConfig(c.Path, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}
