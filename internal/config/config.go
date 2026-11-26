// Package config persists provider settings and manages provider credentials.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

func NewConfigReader() *ConfigReader {
	return &ConfigReader{Path: configFilePath()}
}

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

func validateProviderName(name string) error {
	if name == "" {
		return errors.New("provider name cannot be empty")
	}
	if name == "." || strings.Contains(name, "..") || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("invalid provider name %q", name)
	}
	return nil
}

func getProviderKeyFilePath(configPath, providerName string) string {
	configPath = os.ExpandEnv(configPath)
	return filepath.Join(filepath.Dir(configPath), ".keys", providerName)
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

// Read config content or default values if the file is absent.
func readConfig(path string) (*Config, error) {
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

func writeConfig(path string, config *Config) error {
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

func lockConfig(path string) (func(), error) {
	path = os.ExpandEnv(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

type credentialSnapshot struct {
	data   []byte
	mode   os.FileMode
	exists bool
}

func snapshotCredential(path string) (credentialSnapshot, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return credentialSnapshot{}, nil
	}
	if err != nil {
		return credentialSnapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return credentialSnapshot{}, err
	}
	return credentialSnapshot{data: data, mode: info.Mode().Perm(), exists: true}, nil
}

func (s credentialSnapshot) restore(path string) error {
	if !s.exists {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, s.data, s.mode); err != nil {
		return err
	}
	return os.Chmod(path, s.mode)
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
func (c *Config) AddInferenceSnapProvider(name string, baseURL string, snap string, api *string) error {
	provider := Provider{
		Enabled:    true,
		Backend:    BackendSnap,
		BaseURL:    baseURL,
		API:        api,
		Snap:       &snap,
		AuthScheme: AuthorizationNone,
	}

	return c.addProvider(name, provider)
}

// AddOpenAIProvider registers an enabled proxy and persists its optional API key.
func (c *ConfigReader) AddOpenAIProvider(name string, baseURL string, apiKey *string) error {
	unlock, err := lockConfig(c.Path)
	if err != nil {
		return fmt.Errorf("locking config: %w", err)
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

	cfg, err := readConfig(c.Path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	err = cfg.addProvider(name, provider)
	if err != nil {
		return fmt.Errorf("adding provider: %w", err)
	}
	keyFilePath := getProviderKeyFilePath(c.Path, name)
	previousKey, err := snapshotCredential(keyFilePath)
	if err != nil {
		return fmt.Errorf("reading key file: %w", err)
	}

	err = cfg.setProviderKey(name, apiKey, c.Path)
	if err != nil {
		return errors.Join(fmt.Errorf("setting provider key: %w", err), previousKey.restore(keyFilePath))
	}

	err = writeConfig(c.Path, cfg)
	if err != nil {
		return errors.Join(fmt.Errorf("writing config: %w", err), previousKey.restore(keyFilePath))
	}

	return nil
}

// RemoveProvider deletes a provider's credential file before removing its settings.
func (c *ConfigReader) RemoveProvider(name string) error {
	unlock, err := lockConfig(c.Path)
	if err != nil {
		return fmt.Errorf("locking config: %w", err)
	}
	defer unlock()

	cfg, err := readConfig(c.Path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	if cfg.Providers == nil {
		return fmt.Errorf("provider %s does not exist", name)
	}

	var provider Provider
	var exists bool
	if provider, exists = cfg.Providers[name]; !exists {
		return fmt.Errorf("provider %s does not exist", name)
	}

	switch provider.AuthScheme {
	case AuthorizationNone:
		// no action needed
	case AuthorizationAPIKey:
		keyFilePath := getProviderKeyFilePath(c.Path, name)
		previousKey, err := snapshotCredential(keyFilePath)
		if err != nil {
			return fmt.Errorf("reading key file: %w", err)
		}
		if err := os.Remove(keyFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("deleting key file: %w", err)
		}
		delete(cfg.Providers, name)
		if err := writeConfig(c.Path, cfg); err != nil {
			return errors.Join(fmt.Errorf("writing config: %w", err), previousKey.restore(keyFilePath))
		}
		return nil
	default:
		return fmt.Errorf("unsupported authorization scheme: %v", provider.AuthScheme)
	}

	delete(cfg.Providers, name)
	if err = writeConfig(c.Path, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// GetProviderKey returns a pointer to the API key, or nil when the provider has no authorization.
func (c *ConfigReader) GetProviderKey(providerName string) (*string, error) {
	cfg, err := readConfig(c.Path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	provider, exists := cfg.Providers[providerName]
	if !exists {
		return nil, fmt.Errorf("provider %s does not exist", providerName)
	}

	switch provider.AuthScheme {
	case AuthorizationNone:
		return nil, nil
	case AuthorizationAPIKey:
		keyFilePath := getProviderKeyFilePath(c.Path, providerName)
		data, err := os.ReadFile(keyFilePath)
		if err != nil {
			return nil, fmt.Errorf("reading key file: %w", err)
		}
		key := strings.TrimSpace(string(data))
		return &key, nil

	default:
		return nil, fmt.Errorf("unsupported authorization scheme: %v", provider.AuthScheme)
	}
}

// SetProviderKey persists credentials for a given provider, or removes them if the key is nil.
func (c *ConfigReader) SetProviderKey(providerName string, key *string) error {
	unlock, err := lockConfig(c.Path)
	if err != nil {
		return fmt.Errorf("locking config: %w", err)
	}
	defer unlock()

	cfg, err := readConfig(c.Path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	keyFilePath := getProviderKeyFilePath(c.Path, providerName)
	previousKey, err := snapshotCredential(keyFilePath)
	if err != nil {
		return fmt.Errorf("reading key file: %w", err)
	}
	if err := cfg.setProviderKey(providerName, key, c.Path); err != nil {
		return errors.Join(err, previousKey.restore(keyFilePath))
	}
	if err := writeConfig(c.Path, cfg); err != nil {
		return errors.Join(err, previousKey.restore(keyFilePath))
	}
	return nil
}

func (c *Config) setProviderKey(providerName string, key *string, configPath string) error {
	if c.Providers == nil {
		return fmt.Errorf("provider %s does not exist", providerName)
	}

	provider, exists := c.Providers[providerName]
	if !exists {
		return fmt.Errorf("provider %s does not exist", providerName)
	}

	if key == nil {
		// remove existing key (if any)
		if provider.AuthScheme != AuthorizationNone {
			// we are removing an existing key
			keyFilePath := getProviderKeyFilePath(configPath, providerName)
			if err := os.Remove(keyFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("deleting key file: %w", err)
			}
			provider.AuthScheme = AuthorizationNone
		}
		c.Providers[providerName] = provider
		return nil
	}

	keyFilePath := getProviderKeyFilePath(configPath, providerName)

	if err := os.MkdirAll(filepath.Dir(keyFilePath), 0o700); err != nil {
		return fmt.Errorf("creating key file directory: %w", err)
	}

	if err := os.WriteFile(keyFilePath, []byte(*key), 0o600); err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}

	provider.AuthScheme = AuthorizationAPIKey
	c.Providers[providerName] = provider
	return nil
}
