package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func getProviderKeyFilePath(configPath, providerName string) (string, error) {
	if err := validateProviderName(providerName); err != nil {
		return "", err
	}
	configPath = os.ExpandEnv(configPath)
	return filepath.Join(filepath.Dir(configPath), ".keys", providerName), nil
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

func removeCredential(path string) (credentialSnapshot, error) {
	previousKey, err := snapshotCredential(path)
	if err != nil {
		return credentialSnapshot{}, fmt.Errorf("reading key file: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return credentialSnapshot{}, fmt.Errorf("deleting key file: %w", err)
	}
	return previousKey, nil
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
		keyFilePath, err := getProviderKeyFilePath(c.Path, providerName)
		if err != nil {
			return nil, err
		}
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
	keyFilePath, err := getProviderKeyFilePath(c.Path, providerName)
	if err != nil {
		return err
	}
	cfg, unlock, err := readConfigLocked(c.Path)
	if err != nil {
		return err
	}
	defer unlock()

	if _, exists := cfg.Providers[providerName]; !exists {
		return fmt.Errorf("provider %s does not exist", providerName)
	}
	previousKey, err := snapshotCredential(keyFilePath)
	if err != nil {
		return fmt.Errorf("reading key file: %w", err)
	}
	if err := cfg.setProviderKey(providerName, key, keyFilePath); err != nil {
		return errors.Join(err, previousKey.restore(keyFilePath))
	}
	if err := writeConfig(c.Path, cfg); err != nil {
		return errors.Join(err, previousKey.restore(keyFilePath))
	}
	return nil
}

func (c *Config) setProviderKey(providerName string, key *string, keyFilePath string) error {
	provider, exists := c.Providers[providerName]
	if !exists {
		return fmt.Errorf("provider %s does not exist", providerName)
	}

	if key == nil {
		if provider.AuthScheme != AuthorizationNone {
			if err := os.Remove(keyFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("deleting key file: %w", err)
			}
			provider.AuthScheme = AuthorizationNone
		}
		c.Providers[providerName] = provider
		return nil
	}

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
