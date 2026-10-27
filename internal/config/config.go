package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Version           int                 `json:"version"`
	DiscoverProviders bool                `json:"discover_providers"`
	Providers         map[string]Provider `json:"providers"`
}

// TODO: this is duplicated from provider.go, needs a refactoring
type ProviderType string

const (
	TypeInferenceSnap ProviderType = "inference-snap"
	TypeOpenAI        ProviderType = "openai"
)

type Provider struct {
	Type    ProviderType `json:"type"`
	BaseUrl string       `json:"base_url"`
	Snap    *string      `json:"snap,omitempty"`     // only applicable for Inference Snaps providers
	KeyFile *string      `json:"key_file,omitempty"` // only applicable for OpenAI providers
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
