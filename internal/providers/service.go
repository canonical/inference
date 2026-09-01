package providers

import (
	"context"
	"fmt"
	"sort"
)

// CatalogSource resolves the public provider catalog to an ordered set of
// authoritative provider names. Implementations own caching, refresh, and
// fallback policy; this package only consumes the resolved names.
type CatalogSource interface {
	// Resolve returns the current provider names along with any non-fatal
	// warnings describing degraded catalog sources (stale cache, embedded
	// seed, and similar). A non-nil error means no usable catalog exists.
	Resolve(ctx context.Context) (names []string, warnings []string, err error)
}

// SnapStatusSource reports installed-snap statuses from snapd, keyed by snap
// name. A name absent from the map means "no matching installed snap". A
// name present with an empty status string is treated as untrustworthy data.
type SnapStatusSource interface {
	Statuses(ctx context.Context) (map[string]string, error)
}

// ListOptions controls provider filtering.
type ListOptions struct {
	// InstalledOnly excludes providers whose status is StatusNotInstalled.
	InstalledOnly bool
}

// Service joins the provider catalog with snapd's installed-snap state.
type Service struct {
	Catalog CatalogSource
	Snaps   SnapStatusSource
}

// NewService constructs a Service from its dependencies.
func NewService(catalog CatalogSource, snaps SnapStatusSource) *Service {
	return &Service{Catalog: catalog, Snaps: snaps}
}

// List returns providers sorted lexically by name, optionally filtered to
// only installed providers. The returned warnings should be surfaced on
// stderr; they never accompany a nil error.
func (s *Service) List(ctx context.Context, opts ListOptions) ([]Provider, []string, error) {
	names, warnings, err := s.Catalog.Resolve(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving provider catalog: %w", err)
	}

	statuses, err := s.Snaps.Statuses(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("reading installed snaps: %w", err)
	}

	providers := make([]Provider, 0, len(names))
	for _, name := range names {
		status, present := statuses[name]
		if present {
			if status == "" {
				return nil, nil, fmt.Errorf("snapd reported an empty status for installed provider %q", name)
			}
		} else {
			status = StatusNotInstalled
		}

		provider := Provider{Name: name, Type: InferenceSnap, Status: status}
		if opts.InstalledOnly && !provider.Installed() {
			continue
		}
		providers = append(providers, provider)
	}

	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })

	return providers, warnings, nil
}
