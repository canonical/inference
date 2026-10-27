package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ProviderBackend string

const (
	BackendSnap  ProviderBackend = "snap"
	BackendProxy ProviderBackend = "proxy"
)

type Config struct {
	Version           int                 `json:"version"`
	DiscoverProviders bool                `json:"discover-providers"`
	Providers         map[string]Provider `json:"providers"`
}

type Provider struct {
	Enabled bool            `json:"enabled"`
	Backend ProviderBackend `json:"backend"`
	BaseUrl string          `json:"base-url"`
	Snap    *string         `json:"snap,omitempty"` // only applicable for Inference Snaps providers
	Api     *string         `json:"api,omitempty"`
	KeyFile *string         `json:"key-file,omitempty"` // only applicable for OpenAI providers
}

const (
	ConfigFilePath = "$SNAP_DATA/config.json"
)

func NewConfig() Config {
	c := Config{
		Version:           1,
		DiscoverProviders: true,
		Providers:         make(map[string]Provider),
	}
	return c
}

func Read(path string) (*Config, error) {
	path = os.ExpandEnv(path)

	// config file does not exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		config := NewConfig()
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

func Write(path string, config *Config) error {
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

func replaceFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

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

func (c *Config) AddInferenceSnapProvider(name string, baseUrl string, snap string, api *string) error {
	provider := Provider{
		Enabled: true,
		Backend: BackendSnap,
		BaseUrl: baseUrl,
		Api:     api,
		Snap:    &snap,
		KeyFile: nil,
	}

	return c.addProvider(name, provider)
}

func (c *Config) AddOpenAIProvider(name string, baseUrl string, apiKey string) error {
	keyFilePath := os.ExpandEnv(fmt.Sprintf("$SNAP_DATA/.keys/%s", name))
	err := os.WriteFile(keyFilePath, []byte(apiKey), 0o600)
	if err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}

	provider := Provider{
		Enabled: true,
		Api:     new("openai"),
		Backend: BackendProxy,
		BaseUrl: baseUrl,
		Snap:    nil,
		KeyFile: &keyFilePath,
	}

	return c.addProvider(name, provider)
}

func (c *Config) RemoveProvider(name string) error {
	if c.Providers == nil {
		return nil
	}

	var provider Provider
	var exists bool
	if provider, exists = c.Providers[name]; !exists {
		return fmt.Errorf("provider %s does not exist", name)
	}

	if provider.KeyFile != nil {
		if err := os.Remove(*provider.KeyFile); err != nil {
			return fmt.Errorf("deleting key file: %v", err)
		}
	}

	delete(c.Providers, name)
	return nil
}

func (c *Config) GetProviderKey(providerName string) (*string, error) {
	if c.Providers == nil {
		return nil, fmt.Errorf("provider %s does not exist", providerName)
	}

	provider, exists := c.Providers[providerName]
	if !exists {
		return nil, fmt.Errorf("provider %s does not exist", providerName)
	}

	if provider.KeyFile == nil {
		return nil, nil
	}

	data, err := os.ReadFile(*provider.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}

	key := string(data)
	return &key, nil
}

func (c *Config) SetProviderKey(providerName string, key *string) error {
	if c.Providers == nil {
		return fmt.Errorf("provider %s does not exist", providerName)
	}

	provider, exists := c.Providers[providerName]
	if !exists {
		return fmt.Errorf("provider %s does not exist", providerName)
	}

	if key == nil && provider.KeyFile != nil {
		// we are removing an existing key
		if err := os.Remove(*provider.KeyFile); err != nil {
			return fmt.Errorf("deleting key file: %v", err)
		}
		provider.KeyFile = nil
		c.Providers[providerName] = provider
		return nil
	}

	keyFilePath := os.ExpandEnv(fmt.Sprintf("$SNAP_DATA/.keys/%s", providerName))
	err := os.WriteFile(keyFilePath, []byte(*key), 0o600)
	if err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}

	provider.KeyFile = &keyFilePath
	c.Providers[providerName] = provider
	return nil
}
