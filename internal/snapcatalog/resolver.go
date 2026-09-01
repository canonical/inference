package snapcatalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/canonical/inference/internal/providers"
)

const seedPathEnvironment = "INFERENCE_SNAP_CATALOG_SEED"

func FetchEntries(ctx context.Context, fetcher Fetcher) ([]Entry, error) {
	data, err := fetcher.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

type Resolver struct {
	Cache    Cache
	Fetcher  Fetcher
	Now      func() time.Time
	SeedPath string
}

func NewResolver() *Resolver {
	path, err := DefaultCachePath()
	if err != nil {
		path = ""
	}
	return &Resolver{
		Cache:    Cache{Path: path},
		Fetcher:  NewHTTPFetcher(),
		Now:      time.Now,
		SeedPath: os.Getenv(seedPathEnvironment),
	}
}

func (r *Resolver) List(ctx context.Context) ([]providers.Definition, []string, error) {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}

	var warnings []string
	var stale *Document
	if r.Cache.Path != "" {
		document, err := r.Cache.Load(now)
		switch {
		case err == nil && now.Sub(document.FetchedAt) <= CacheTTL:
			return definitions(document.Entries), nil, nil
		case err == nil:
			stale = &document
		case !errors.Is(err, os.ErrNotExist):
			warnings = append(warnings, fmt.Sprintf("ignoring invalid snap catalog cache: %v", err))
		}
	}

	entries, refreshErr := FetchEntries(ctx, r.Fetcher)
	if refreshErr == nil {
		document := Document{
			SchemaVersion: SchemaVersion,
			FetchedAt:     now,
			Entries:       entries,
		}
		if r.Cache.Path != "" {
			if err := r.Cache.Save(document); err != nil {
				warnings = append(warnings, fmt.Sprintf("using refreshed snap catalog but could not persist cache: %v", err))
			}
		}
		return definitions(document.Entries), warnings, nil
	}

	if stale != nil {
		warnings = append(warnings, fmt.Sprintf("using stale snap catalog cache because refresh failed: %v", refreshErr))
		return definitions(stale.Entries), warnings, nil
	}

	if r.SeedPath == "" {
		return nil, nil, fmt.Errorf("refreshing snap catalog: %v; snap catalog seed path is not configured", refreshErr)
	}
	seedData, err := os.ReadFile(r.SeedPath)
	if err != nil {
		return nil, nil, fmt.Errorf("refreshing snap catalog: %v; reading snap catalog seed: %w", refreshErr, err)
	}
	seed, err := DecodeDocument(seedData)
	if err != nil {
		return nil, nil, fmt.Errorf("refreshing snap catalog: %v; loading snap catalog seed: %w", refreshErr, err)
	}
	warnings = append(warnings, fmt.Sprintf("using packaged snap catalog seed because refresh failed: %v", refreshErr))
	return definitions(seed.Entries), warnings, nil
}

func definitions(entries []Entry) []providers.Definition {
	result := make([]providers.Definition, len(entries))
	for i, entry := range entries {
		result[i] = providers.Definition{
			Name: entry.SnapName,
			Type: providers.TypeInferenceSnap,
		}
	}
	return result
}
