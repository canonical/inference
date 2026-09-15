package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

type ProviderType string

const (
	TypeInferenceSnap ProviderType = "inference-snap"
	TypeOpenAI        ProviderType = "openai"
)

type LifecycleState string

const (
	StateUnknown      LifecycleState = "unknown"
	StateNotInstalled LifecycleState = "not installed"
	StateDisabled     LifecycleState = "disabled"
	StateEnabled      LifecycleState = "enabled"
)

type ConnectionState string

const (
	ConnectionNotConnected  ConnectionState = "not connected"
	ConnectionConnected     ConnectionState = "connected"
	ConnectionNotApplicable ConnectionState = "not applicable"
)

type Provider struct {
	Name       string
	Type       ProviderType
	State      LifecycleState
	Connection ConnectionState
	BaseURL    string
}

type ProviderIdentity struct {
	Type ProviderType
	Name string
}

func (p Provider) Installed() bool {
	switch p.Type {
	case TypeInferenceSnap:
		return p.State == StateDisabled || p.State == StateEnabled
	case TypeOpenAI:
		return true
	default:
		return false
	}
}

func (p Provider) Identity() ProviderIdentity {
	return ProviderIdentity{Type: p.Type, Name: p.Name}
}

type ListOptions struct {
	InstalledOnly bool
	Name          string
}

func Find(
	ctx context.Context,
	catalog *snapcatalog.Reader,
	snapdClient *snapd.Client,
	shareProvidersPath string,
	name string,
) (Provider, error) {
	if name == "" {
		return Provider{}, fmt.Errorf("provider name can't be empty")
	}

	matches, err := List(ctx, catalog, snapdClient, shareProvidersPath, ListOptions{Name: name})
	if err != nil {
		return Provider{}, err
	}

	switch len(matches) {
	case 0:
		return Provider{}, fmt.Errorf("unknown provider %q", name)
	case 1:
		return matches[0], nil
	default:
		// only to be hit when we add setup for types other than TypeInferenceSnap (e.g. TypeOpenAI)
		return Provider{}, fmt.Errorf("ambiguous provider %q: matches multiple provider types", name)
	}
}

func List(
	ctx context.Context,
	catalog *snapcatalog.Reader,
	snapdClient *snapd.Client,
	shareProvidersPath string,
	options ListOptions,
) ([]Provider, error) {

	catalogProviders, err := CatalogSnapProviders(catalog)
	if errors.Is(err, snapcatalog.ErrNotConfigured) || errors.Is(err, os.ErrNotExist) {
		catalogProviders = []Provider{}
	} else if err != nil {
		return nil, err
	}

	connectedProviders, err := ConnectedSnapProviders(shareProvidersPath)
	if err != nil {
		return nil, err
	}

	configuredProviders, err := ConfiguredOpenAIProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading configured OpenAI providers: %w", err)
	}

	// Union catalog and connected inference snap providers
	merged := make(map[ProviderIdentity]Provider)
	for _, provider := range catalogProviders {
		merged[provider.Identity()] = provider
	}
	for _, provider := range connectedProviders {
		key := provider.Identity()
		if existing, ok := merged[key]; ok {
			existing.Connection = provider.Connection
			existing.BaseURL = provider.BaseURL
			merged[key] = existing
			continue
		}
		merged[key] = provider
	}
	for _, provider := range configuredProviders {
		merged[provider.Identity()] = provider
	}

	// Add snap state and optionally filter for only installed providers
	result := make([]Provider, 0, len(merged))
	for _, provider := range merged {
		if options.Name != "" && provider.Name != options.Name {
			continue
		}
		if provider.Type == TypeInferenceSnap {
			state, err := snapProviderLifecycleState(ctx, snapdClient, provider.Name)
			if err != nil {
				return nil, err
			}
			provider.State = state
		}

		if options.InstalledOnly && !provider.Installed() {
			continue
		}
		result = append(result, provider)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Type == result[j].Type {
			return result[i].Name < result[j].Name
		}
		return result[i].Type < result[j].Type
	})
	return result, nil
}

func snapProviderLifecycleState(ctx context.Context, snapdClient *snapd.Client, name string) (LifecycleState, error) {
	if snapdClient == nil {
		return StateUnknown, fmt.Errorf("reading snap status for %q: snapd client is not configured", name)
	}
	snapStatus, err := snapdClient.Status(ctx, name)
	if err != nil {
		return StateUnknown, fmt.Errorf("reading snap status for %q: %w", name, err)
	}
	state, err := snapStatusToProviderState(snapStatus)
	if err != nil {
		return StateUnknown, fmt.Errorf("reading snap status for %q: %w", name, err)
	}
	return state, nil
}

func snapStatusToProviderState(snapStatus string) (LifecycleState, error) {
	switch snapStatus {
	case snapd.StatusNotInstalled:
		return StateNotInstalled, nil
	case snapd.SnapStatusActive:
		return StateEnabled, nil
	case snapd.SnapStatusInstalled:
		return StateDisabled, nil
	default:
		return StateUnknown, fmt.Errorf("unexpected snapd status %q", snapStatus)
	}
}
