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

	providersByName := make(map[string]Provider, len(entries))
	var unverified []Provider
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		provider, found := readConnectedProvider(root, entry.Name())
		if !found {
			continue
		}
		if provider.nameUnverified {
			unverified = append(unverified, provider)
			continue
		}
		if existing, exists := providersByName[provider.Name]; exists {
			if existing.Err != nil && provider.Err == nil {
				providersByName[provider.Name] = provider
				provider = existing
			}
			provider = demoteDuplicate(provider)
		}
		providersByName[provider.Name] = provider
	}
	for _, provider := range unverified {
		if _, exists := providersByName[provider.Name]; exists {
			provider.Name = invalidDisplayName(provider.Name)
		}
		providersByName[provider.Name] = provider
	}

	providers := make([]Provider, 0, len(providersByName))
	for _, provider := range providersByName {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	return providers, nil
}

// A provider that cannot be read is named after its directory so it can still be reported.
func readConnectedProvider(root, directoryName string) (Provider, bool) {
	envPath := filepath.Join(root, directoryName, "provider.env")
	values, err := godotenv.Read(envPath)
	if errors.Is(err, os.ErrNotExist) {
		return Provider{}, false
	}

	name := values["SNAP_NAME"]
	baseURL := values["OPENAI_BASE_URL"]
	if err == nil && name == "" {
		err = errors.New("SNAP_NAME is empty")
	}
	if err == nil {
		err = verifyBaseURL(baseURL)
	}
	nameUnverified := name == ""
	if nameUnverified {
		name = directoryName
	}

	provider := Provider{
		Name:       name,
		Backend:    BackendSnap,
		State:      StateUnknown,
		Connection: ConnectionConnected,
		BaseURL:    baseURL,

		nameUnverified: nameUnverified,
	}
	if err != nil {
		provider.BaseURL = ""
		provider.Err = fmt.Errorf("%s: %w", envPath, err)
	}
	return provider, true
}

func demoteDuplicate(provider Provider) Provider {
	if provider.Err == nil {
		provider.Err = fmt.Errorf("duplicate provider name %q", provider.Name)
	}
	provider.BaseURL = ""
	provider.Name = invalidDisplayName(provider.Name)
	provider.nameUnverified = true
	return provider
}

func invalidDisplayName(name string) string {
	return name + " (invalid)"
}

func verifyBaseURL(value string) error {
	if value == "" {
		return fmt.Errorf("OPENAI_BASE_URL is empty")
	}
	baseURL, err := url.Parse(value)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
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
