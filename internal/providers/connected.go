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

const ShareProvidersEnvVar = "INFERENCE_SHARE_PROVIDERS"

func DefaultShareProvidersPath() string {
	if path := os.Getenv(ShareProvidersEnvVar); path != "" {
		return path
	}
	if snapRoot := os.Getenv("SNAP"); snapRoot != "" {
		return filepath.Join(snapRoot, "share/providers")
	}
	return ""
}

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
		name := values["SNAP_NAME"]
		if name == "" {
			return nil, fmt.Errorf("reading connected provider %q: SNAP_NAME is empty", directoryName)
		}
		if _, exists := providersByName[name]; exists {
			return nil, fmt.Errorf("reading connected provider %q: duplicate SNAP_NAME %q", directoryName, name)
		}

		baseURL := values["OPENAI_BASE_URL"]
		if err := verifyBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("reading connected provider %q: %w", directoryName, err)
		}

		providersByName[name] = Provider{
			Name:       name,
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
