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

type connectedProvider struct {
	Provider
	envPath string
}

func ConnectedSnapProviders(root string) ([]Provider, []Issue, error) {
	connected, issues, err := readConnectedProviders(root)
	if err != nil {
		return nil, nil, err
	}
	providers := make([]Provider, len(connected))
	for i, provider := range connected {
		providers[i] = provider.Provider
	}
	return providers, issues, nil
}

func readConnectedProviders(root string) ([]connectedProvider, []Issue, error) {
	if root == "" {
		return nil, nil, nil
	}

	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading connected provider directory %q: %w", root, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	connected := make([]connectedProvider, 0, len(entries))
	issues := []Issue{}
	envPathsByName := make(map[string]string, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		provider, err := readConnectedProvider(filepath.Join(root, entry.Name()))
		if err != nil {
			issues = append(issues, newSharedProviderIssue("%v", err))
			continue
		}
		if firstPath, exists := envPathsByName[provider.Name]; exists {
			issues = append(issues, newSharedProviderIssue(
				"duplicate provider name %q in %s, already defined in %s", provider.Name, provider.envPath, firstPath,
			))
			continue
		}
		envPathsByName[provider.Name] = provider.envPath
		connected = append(connected, provider)
	}
	sort.Slice(connected, func(i, j int) bool { return connected[i].Name < connected[j].Name })
	return connected, issues, nil
}

func readConnectedProvider(directory string) (connectedProvider, error) {
	envPath := filepath.Join(directory, "provider.env")
	values, err := godotenv.Read(envPath)
	if errors.Is(err, os.ErrNotExist) {
		return connectedProvider{}, fmt.Errorf("missing provider.env in %s/", directory)
	}
	if err != nil {
		return connectedProvider{}, fmt.Errorf("cannot read %s: %w", envPath, err)
	}

	name := values["SNAP_NAME"]
	if name == "" {
		return connectedProvider{}, fmt.Errorf("missing SNAP_NAME in %s", envPath)
	}
	baseURL := values["OPENAI_BASE_URL"]
	if baseURL == "" {
		return connectedProvider{}, fmt.Errorf("missing OPENAI_BASE_URL in %s", envPath)
	}
	if err := verifyBaseURL(baseURL); err != nil {
		return connectedProvider{}, fmt.Errorf("invalid OPENAI_BASE_URL in %s: %w", envPath, err)
	}

	return connectedProvider{
		Provider: Provider{
			Name:       name,
			Backend:    BackendSnap,
			State:      StateUnknown,
			Connection: ConnectionConnected,
			BaseURL:    baseURL,
		},
		envPath: envPath,
	}, nil
}

func verifyBaseURL(value string) error {
	baseURL, err := url.Parse(value)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("cannot be parsed: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if baseURL.Host == "" {
		return errors.New("has no host")
	}
	return nil
}

func identifyNamingCollisions(connected []connectedProvider, configured []Provider) ([]Provider, []Issue) {
	configuredNames := make(map[string]struct{}, len(configured))
	for _, provider := range configured {
		configuredNames[provider.Name] = struct{}{}
	}

	accepted := make([]Provider, 0, len(connected))
	issues := []Issue{}
	for _, provider := range connected {
		if _, collides := configuredNames[provider.Name]; collides {
			issues = append(issues, newSharedProviderIssue(
				"provider name %q in %s is already used by a configured provider", provider.Name, provider.envPath,
			))
			continue
		}
		accepted = append(accepted, provider.Provider)
	}
	return accepted, issues
}
