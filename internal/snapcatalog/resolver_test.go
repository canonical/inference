package snapcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type countingFetcher struct {
	data  []byte
	err   error
	calls int
}

func (f *countingFetcher) Fetch(context.Context) ([]byte, error) {
	f.calls++
	return f.data, f.err
}

func encodedTestSeed(t *testing.T, at time.Time) []byte {
	t.Helper()
	data, err := EncodeDocument(testDocument(at))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeTestSeed(t *testing.T, at time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(path, encodedTestSeed(t, at), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolverUsesFreshCacheWithoutRefresh(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cache := Cache{Path: filepath.Join(t.TempDir(), "snap-catalog.json")}
	if err := cache.Save(testDocument(now.Add(-CacheTTL))); err != nil {
		t.Fatal(err)
	}
	fetcher := &countingFetcher{err: errors.New("must not be called")}
	resolver := &Resolver{Cache: cache, Fetcher: fetcher, Now: func() time.Time { return now }}

	definitions, warnings, err := resolver.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 || len(warnings) != 0 || len(definitions) != 2 {
		t.Fatalf("calls=%d warnings=%v definitions=%v", fetcher.calls, warnings, definitions)
	}
}

func TestResolverRefreshesStaleCacheAndReplacesIt(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cache := Cache{Path: filepath.Join(t.TempDir(), "snap-catalog.json")}
	if err := cache.Save(testDocument(now.Add(-CacheTTL - time.Second))); err != nil {
		t.Fatal(err)
	}
	fetcher := &countingFetcher{data: []byte(catalogWithRows(
		catalogRow("new-snap", "New", "canonical/new-snap", "https://github.com/canonical/new-snap"),
	))}
	resolver := &Resolver{
		Cache:   cache,
		Fetcher: fetcher,
		Now:     func() time.Time { return now },
	}

	definitions, warnings, err := resolver.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || len(definitions) != 1 || definitions[0].Name != "new-snap" {
		t.Fatalf("warnings=%v definitions=%v", warnings, definitions)
	}
	document, err := cache.Load(now)
	if err != nil {
		t.Fatal(err)
	}
	if document.Entries[0].SnapName != "new-snap" {
		t.Fatalf("cache was not replaced: %+v", document)
	}
}

func TestResolverUsesStaleCacheAfterFailedRefresh(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cache := Cache{Path: filepath.Join(t.TempDir(), "snap-catalog.json")}
	if err := cache.Save(testDocument(now.Add(-2 * CacheTTL))); err != nil {
		t.Fatal(err)
	}
	resolver := &Resolver{
		Cache:   cache,
		Fetcher: &countingFetcher{err: errors.New("offline")},
		Now:     func() time.Time { return now },
	}

	definitions, warnings, err := resolver.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 2 || len(warnings) != 1 || !strings.Contains(warnings[0], "stale") {
		t.Fatalf("warnings=%v definitions=%v", warnings, definitions)
	}
}

func TestResolverUsesSeedAfterFailedRefreshWithoutCache(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	resolver := &Resolver{
		Cache:    Cache{Path: filepath.Join(t.TempDir(), "missing.json")},
		Fetcher:  &countingFetcher{err: errors.New("offline")},
		Now:      func() time.Time { return now },
		SeedPath: writeTestSeed(t, now.Add(-24*time.Hour)),
	}

	definitions, warnings, err := resolver.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 2 || len(warnings) != 1 || !strings.Contains(warnings[0], "packaged") {
		t.Fatalf("warnings=%v definitions=%v", warnings, definitions)
	}
}

func TestResolverWarnsForMalformedCacheThenUsesSeed(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "snap-catalog.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := &Resolver{
		Cache:    Cache{Path: path},
		Fetcher:  &countingFetcher{err: errors.New("offline")},
		Now:      func() time.Time { return now },
		SeedPath: writeTestSeed(t, now),
	}

	_, warnings, err := resolver.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "invalid snap catalog cache") {
		t.Fatalf("warnings=%v", warnings)
	}
}

func TestResolverUsesRefreshWhenCacheWriteFails(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := &Resolver{
		Cache: Cache{Path: filepath.Join(parentFile, "snap-catalog.json")},
		Fetcher: &countingFetcher{data: []byte(catalogWithRows(
			catalogRow("new-snap", "New", "canonical/new-snap", "https://github.com/canonical/new-snap"),
		))},
		Now: func() time.Time { return now },
	}

	definitions, warnings, err := resolver.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 || definitions[0].Name != "new-snap" {
		t.Fatalf("definitions=%v", definitions)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[len(warnings)-1], "could not persist") {
		t.Fatalf("warnings=%v", warnings)
	}
}

func TestResolverFailsWhenRefreshAndSeedAreUnusable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := &Resolver{
		Fetcher:  &countingFetcher{err: errors.New("offline")},
		SeedPath: path,
	}
	if _, _, err := resolver.List(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewResolverUsesConfiguredSeedPath(t *testing.T) {
	t.Setenv(seedPathEnvironment, "/snap/inference/current/share/inference/snap-catalog-seed.json")
	if path := NewResolver().SeedPath; path != "/snap/inference/current/share/inference/snap-catalog-seed.json" {
		t.Fatalf("SeedPath=%q", path)
	}
}
