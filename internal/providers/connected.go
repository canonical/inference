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

func ConnectedSnapProviders(root string) ([]Provider, error) {
	if root == "" {
		return []Provider{}, nil
	}

	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []Provider{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading connected provider directory %q: %w", root, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	providersByName := make(map[string]Provider, len(entries))
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
		if err != nil {
			return nil, fmt.Errorf("reading connected provider %q: %w", directoryName, err)
		}
		snapName := values["SNAP_NAME"]
		if snapName == "" {
			return nil, fmt.Errorf("reading connected provider %q: SNAP_NAME is empty", directoryName)
		}
		instanceName := values["SNAP_INSTANCE_NAME"]
		if instanceName == "" {
			instanceName = snapName
		}
		if _, exists := providersByName[instanceName]; exists {
			return nil, fmt.Errorf("reading connected provider %q: duplicate SNAP_INSTANCE_NAME %q", directoryName, instanceName)
		}

		baseURL := values["OPENAI_BASE_URL"]
		if err := verifyBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("reading connected provider %q: %w", directoryName, err)
		}

		providersByName[instanceName] = Provider{
			Name:       instanceName,
			SnapName:   snapName,
			Type:       TypeInferenceSnap,
			State:      StateUnknown,
			Connection: ConnectionConnected,
			BaseURL:    baseURL,
		}
	}

	providers := make([]Provider, 0, len(providersByName))
	for _, provider := range providersByName {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	return providers, nil
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
