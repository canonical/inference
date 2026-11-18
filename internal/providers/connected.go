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
	envPaths := make(map[string]string, len(entries))
	var unverified []Provider
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		provider, envPath, found := readConnectedProvider(root, entry.Name())
		if !found {
			continue
		}
		if provider.nameUnverified {
			unverified = append(unverified, provider)
			continue
		}
		if kept, exists := providersByName[provider.Name]; exists {
			keptPath := envPaths[kept.Name]
			if kept.Err != nil && provider.Err == nil {
				kept, provider = provider, kept
				keptPath, envPath = envPath, keptPath
			}
			providersByName[kept.Name] = kept
			envPaths[kept.Name] = keptPath
			provider = demoteDuplicate(provider, envPath, keptPath, func(name string) bool {
				_, taken := providersByName[name]
				return taken
			})
		}
		providersByName[provider.Name] = provider
		envPaths[provider.Name] = envPath
	}
	for _, provider := range unverified {
		if _, exists := providersByName[provider.Name]; exists {
			provider.Name = duplicateDisplayName(provider.Name, func(name string) bool {
				_, taken := providersByName[name]
				return taken
			})
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
func readConnectedProvider(root, directoryName string) (Provider, string, bool) {
	envPath := filepath.Join(root, directoryName, "provider.env")
	values, err := godotenv.Read(envPath)
	if errors.Is(err, os.ErrNotExist) {
		return Provider{}, "", false
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
	return provider, envPath, true
}

func demoteDuplicate(provider Provider, envPath, keptPath string, taken func(string) bool) Provider {
	if provider.Err == nil {
		provider.Err = fmt.Errorf(
			"%s: duplicate provider name %q, already defined in %s",
			envPath, provider.Name, keptPath,
		)
	}
	provider.BaseURL = ""
	provider.Name = duplicateDisplayName(provider.Name, taken)
	provider.nameUnverified = true
	return provider
}

func duplicateDisplayName(name string, taken func(string) bool) string {
	candidate := name + " (duplicate)"
	for i := 2; taken(candidate); i++ {
		candidate = fmt.Sprintf("%s (duplicate %d)", name, i)
	}
	return candidate
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
