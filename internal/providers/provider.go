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

type ProviderBackend string

const (
	BackendSnap  ProviderBackend = "snap"
	BackendProxy ProviderBackend = "proxy"
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

// Engine represents an inference provider's runtime and model details
type Engine struct {
	Name         string
	Runtime      string
	DefaultModel string
	Models       []string
}

type Provider struct {
	Name       string
	Backend    ProviderBackend
	State      LifecycleState
	Connection ConnectionState
	BaseURL    string
	Engines    []Engine
}

type ProviderIdentity struct {
	Backend ProviderBackend
	Name    string
}

func (p Provider) Installed() bool {
	switch p.Backend {
	case BackendSnap:
		return p.State == StateDisabled || p.State == StateEnabled
	case BackendProxy:
		return true
	default:
		return false
	}
}

func (p Provider) Identity() ProviderIdentity {
	return ProviderIdentity{Backend: p.Backend, Name: p.Name}
}

type listOptions struct {
	InstalledOnly bool
	Name          string
}

type Service interface {
	List(ctx context.Context) ([]Provider, error)
	ListInstalled(ctx context.Context) ([]Provider, error)
	Find(ctx context.Context, name string) (Provider, error)
}

type service struct {
	catalog            *snapcatalog.Reader
	snapdClient        *snapd.Client
	shareProvidersPath string
}

func NewService(catalog *snapcatalog.Reader, snapdClient *snapd.Client, shareProvidersPath string) Service {
	return &service{
		catalog:            catalog,
		snapdClient:        snapdClient,
		shareProvidersPath: shareProvidersPath,
	}
}

func (s *service) Find(ctx context.Context, name string) (Provider, error) {
	if name == "" {
		return Provider{}, fmt.Errorf("provider name can't be empty")
	}

	matches, err := s.list(ctx, listOptions{Name: name})
	if err != nil {
		return Provider{}, err
	}

	switch len(matches) {
	case 0:
		return Provider{}, fmt.Errorf("unknown provider %q", name)
	case 1:
		return matches[0], nil
	default:
		return Provider{}, fmt.Errorf("ambiguous provider %q: matches multiple provider backends", name)
	}
}

func (s *service) ListInstalled(ctx context.Context) ([]Provider, error) {
	return s.list(ctx, listOptions{InstalledOnly: true})
}

func (s *service) List(ctx context.Context) ([]Provider, error) {
	return s.list(ctx, listOptions{})
}

func (s *service) list(ctx context.Context, options listOptions) ([]Provider, error) {
	catalogProviders, err := CatalogSnapProviders(s.catalog)
	if errors.Is(err, snapcatalog.ErrNotConfigured) || errors.Is(err, os.ErrNotExist) {
		catalogProviders = []Provider{}
	} else if err != nil {
		return nil, err
	}

	connectedProviders, err := ConnectedSnapProviders(s.shareProvidersPath)
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
		if provider.Backend == BackendSnap {
			state, err := snapProviderLifecycleState(ctx, s.snapdClient, provider.Name)
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
		if result[i].Backend == result[j].Backend {
			return result[i].Name < result[j].Name
		}
		return result[i].Backend < result[j].Backend
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
