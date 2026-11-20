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

	registry := connectedRegistry{
		byName:   make(map[string]Provider, len(entries)),
		envPaths: make(map[string]string, len(entries)),
	}
	var unverified []Provider
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		provider, envPath := readConnectedProvider(root, entry.Name())
		if provider.nameUnverified {
			unverified = append(unverified, provider)
			continue
		}
		registry.addVerified(provider, envPath)
	}
	for _, provider := range unverified {
		registry.addUnverified(provider)
	}

	providers := make([]Provider, 0, len(registry.byName))
	for _, provider := range registry.byName {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	return providers, nil
}

type connectedRegistry struct {
	byName   map[string]Provider
	envPaths map[string]string
}

func (r *connectedRegistry) isTaken(name string) bool {
	_, taken := r.byName[name]
	return taken
}

// When two providers claim the same SNAP_NAME, a working provider keeps the name over a broken one,
// so that a misconfigured entry cannot take down a healthy provider. Otherwise the first one wins.
// The other entry stays visible under a different name, so that its error is still reported.
func (r *connectedRegistry) addVerified(provider Provider, envPath string) {
	if kept, exists := r.byName[provider.Name]; exists {
		keptPath := r.envPaths[kept.Name]
		if kept.Err != nil && provider.Err == nil {
			kept, provider = provider, kept
			keptPath, envPath = envPath, keptPath
		}
		r.byName[kept.Name] = kept
		r.envPaths[kept.Name] = keptPath
		provider = demoteDuplicate(provider, envPath, keptPath, r.isTaken)
	}
	r.byName[provider.Name] = provider
	r.envPaths[provider.Name] = envPath
}

// Entries named after their directory are added last, so they never take a name from a verified provider.
func (r *connectedRegistry) addUnverified(provider Provider) {
	if r.isTaken(provider.Name) {
		provider.Name = duplicateDisplayName(provider.Name, r.isTaken)
	}
	r.byName[provider.Name] = provider
}

// Problems with the directory or the file content are reported in Provider.Err. A provider that
// cannot be read, or has no provider.env, is named after its directory so it can still be reported.
func readConnectedProvider(root, directoryName string) (Provider, string) {
	envPath := filepath.Join(root, directoryName, "provider.env")
	values, err := godotenv.Read(envPath)

	name := values["SNAP_NAME"]
	baseURL := values["OPENAI_BASE_URL"]
	if err == nil && name == "" {
		err = errors.New("SNAP_NAME is empty")
	}
	if err == nil {
		err = verifyBaseURL(baseURL)
	}
	nameUnverified := false
	if name == "" {
		nameUnverified = true
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
		if errors.Is(err, os.ErrNotExist) {
			provider.Err = fmt.Errorf("%s does not have a provider.env file", filepath.Dir(envPath))
		} else {
			provider.Err = fmt.Errorf("%s: %w", envPath, err)
		}
	}
	return provider, envPath
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
