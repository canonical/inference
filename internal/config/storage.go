package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func readConfig(path string) (*Config, error) {
	path = os.ExpandEnv(path)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		config := newConfig()
		return &config, nil
	}
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
	if err := replaceFile(path, data, 0o644); err != nil {
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

func readConfigLocked(path string) (*Config, func(), error) {
	unlock, err := lockConfig(path)
	if err != nil {
		return nil, nil, fmt.Errorf("locking config: %w", err)
	}
	cfg, err := readConfig(path)
	if err != nil {
		unlock()
		return nil, nil, fmt.Errorf("reading config: %w", err)
	}
	return cfg, unlock, nil
}

func replaceFile(path string, data []byte, mode os.FileMode) (err error) {
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

	if err := file.Chmod(mode); err != nil {
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
