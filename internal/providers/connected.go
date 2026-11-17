package providers

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/joho/godotenv"
)

type connectedEntry struct {
	provider  ListedProvider
	directory string
}

func ConnectedSnapProviders(root string) ([]ListedProvider, error) {
	if root == "" {
		return []ListedProvider{}, nil
	}

	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []ListedProvider{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading connected provider directory %q: %w", root, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	entriesByName := make(map[string]connectedEntry, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		directoryName := entry.Name()
		path := filepath.Join(root, directoryName, "provider.env")
		values, err := godotenv.Read(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		name := values["SNAP_NAME"]
		if err == nil && name == "" {
			err = errors.New("SNAP_NAME is empty")
		}
		if err != nil {
			addConnectedEntry(entriesByName, directoryName, directoryName, "", fmt.Errorf("reading connected provider %q: %w", directoryName, err))
			continue
		}

		baseURL := values["OPENAI_BASE_URL"]
		if err := verifyBaseURL(baseURL); err != nil {
			addConnectedEntry(entriesByName, directoryName, name, "", fmt.Errorf("reading connected provider %q: %w", directoryName, err))
			continue
		}
		addConnectedEntry(entriesByName, directoryName, name, baseURL, nil)
	}

	providers := make([]ListedProvider, 0, len(entriesByName))
	for _, entry := range entriesByName {
		providers = append(providers, entry.provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	return providers, nil
}

func addConnectedEntry(entriesByName map[string]connectedEntry, directory, name, baseURL string, err error) {
	provider := ListedProvider{
		Provider: Provider{
			Name:       name,
			Backend:    BackendSnap,
			State:      StateUnknown,
			Connection: ConnectionConnected,
			BaseURL:    baseURL,
		},
		Err: err,
	}
	if existing, exists := entriesByName[name]; exists {
		provider.BaseURL = ""
		provider.Err = fmt.Errorf("duplicate provider name %q in directories %q and %q", name, existing.directory, directory)
		directory = existing.directory
	}
	entriesByName[name] = connectedEntry{provider: provider, directory: directory}
}

func verifyBaseURL(value string) error {
	if value == "" {
		return fmt.Errorf("OPENAI_BASE_URL is empty")
	}
	baseURL, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("parsing OPENAI_BASE_URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return fmt.Errorf("OPENAI_BASE_URL scheme must be http or https")
	}
	if baseURL.Host == "" {
		return fmt.Errorf("OPENAI_BASE_URL has no host")
	}
	return nil
}
