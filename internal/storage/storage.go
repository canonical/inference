// Package storage persists provider settings.
package storage

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Document struct {
	Providers map[string]Provider `json:"providers"`
}

type Provider struct {
	Enabled bool `json:"enabled"`
}

// The file path is expanded against the snap environment when read or written.
const (
	filePathEnvVar  = "INFERENCE_STORAGE_FILE"
	defaultFilePath = "$SNAP_DATA/storage.json"
)

var ErrProviderNotFound = errors.New("does not exist")
var ErrProviderAlreadyExists = errors.New("already exists")

type Storage struct {
	Path string
}

// New returns a Storage initialized with the default file path.
func New() *Storage {
	return &Storage{Path: filePath()}
}

func newDocument() Document {
	return Document{
		Providers: make(map[string]Provider),
	}
}

func filePath() string {
	customFilePath := os.Getenv(filePathEnvVar)
	if customFilePath != "" {
		return os.ExpandEnv(customFilePath)
	}
	return os.ExpandEnv(defaultFilePath)
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

func (s *Storage) GetProviders() (map[string]Provider, error) {
	doc, err := readDocument(s.Path)
	if err != nil {
		return nil, fmt.Errorf("reading storage: %w", err)
	}
	return doc.Providers, nil
}

func (s *Storage) ProviderExists(name string) (bool, error) {
	providers, err := s.GetProviders()
	if err != nil {
		return false, err
	}
	_, exists := providers[name]
	return exists, nil
}

func (d *Document) addProvider(name string, provider Provider) error {
	if err := validateProviderName(name); err != nil {
		return err
	}

	if d.Providers == nil {
		d.Providers = make(map[string]Provider)
	}

	if _, exists := d.Providers[name]; exists {
		return fmt.Errorf("provider %s %w", name, ErrProviderAlreadyExists)
	}

	d.Providers[name] = provider
	return nil
}

func (s *Storage) AddProvider(name string, enabled bool) error {
	doc, unlock, err := readDocumentLocked(s.Path)
	if err != nil {
		return err
	}
	defer unlock()

	if err := doc.addProvider(name, Provider{Enabled: enabled}); err != nil {
		return fmt.Errorf("adding provider: %w", err)
	}
	return writeDocument(s.Path, doc)
}

func (s *Storage) EnsureProviders(names []string) (map[string]Provider, error) {
	doc, unlock, err := readDocumentLocked(s.Path)
	if err != nil {
		return nil, err
	}
	defer unlock()

	changed := false
	for _, name := range names {
		err := doc.addProvider(name, Provider{Enabled: true})
		if errors.Is(err, ErrProviderAlreadyExists) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("adding provider %s: %w", name, err)
		}
		changed = true
	}

	if changed {
		if err := writeDocument(s.Path, doc); err != nil {
			return nil, err
		}
	}
	return doc.Providers, nil
}

// SetProviderEnabled treats a provider missing from the storage as enabled.
func (s *Storage) SetProviderEnabled(name string, enabled bool) (changed bool, err error) {
	if err := validateProviderName(name); err != nil {
		return false, err
	}
	doc, unlock, err := readDocumentLocked(s.Path)
	if err != nil {
		return false, err
	}
	defer unlock()

	provider, exists := doc.Providers[name]
	if !exists {
		provider.Enabled = true
	}
	changed = provider.Enabled != enabled
	if exists && !changed {
		return false, nil
	}
	if doc.Providers == nil {
		doc.Providers = make(map[string]Provider)
	}
	doc.Providers[name] = Provider{Enabled: enabled}
	if err := writeDocument(s.Path, doc); err != nil {
		return false, err
	}
	return changed, nil
}

func (s *Storage) RemoveProvider(name string) error {
	doc, unlock, err := readDocumentLocked(s.Path)
	if err != nil {
		return err
	}
	defer unlock()

	if _, exists := doc.Providers[name]; !exists {
		return fmt.Errorf("provider %s %w", name, ErrProviderNotFound)
	}
	delete(doc.Providers, name)
	if err := writeDocument(s.Path, doc); err != nil {
		return fmt.Errorf("writing storage: %w", err)
	}
	return nil
}
