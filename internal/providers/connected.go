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

	providersByName := make(map[string]ListedProvider, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		provider, found := readConnectedProvider(root, entry.Name())
		if !found {
			continue
		}
		if _, exists := providersByName[provider.Name]; exists {
			provider.BaseURL = ""
			provider.Err = fmt.Errorf("duplicate provider name %q", provider.Name)
		}
		providersByName[provider.Name] = provider
	}

	providers := make([]ListedProvider, 0, len(providersByName))
	for _, provider := range providersByName {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	return providers, nil
}

// A provider that cannot be read is named after its directory so it can still be reported.
func readConnectedProvider(root, directoryName string) (ListedProvider, bool) {
	values, err := godotenv.Read(filepath.Join(root, directoryName, "provider.env"))
	if errors.Is(err, os.ErrNotExist) {
		return ListedProvider{}, false
	}

	name := values["SNAP_NAME"]
	baseURL := values["OPENAI_BASE_URL"]
	if err == nil && name == "" {
		err = errors.New("SNAP_NAME is empty")
	}
	if err == nil {
		err = verifyBaseURL(baseURL)
	}
	if name == "" {
		name = directoryName
	}

	provider := ListedProvider{Provider: Provider{
		Name:       name,
		Backend:    BackendSnap,
		State:      StateUnknown,
		Connection: ConnectionConnected,
		BaseURL:    baseURL,
	}}
	if err != nil {
		provider.BaseURL = ""
		provider.Err = fmt.Errorf("reading connected provider %q: %w", directoryName, err)
	}
	return provider, true
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
