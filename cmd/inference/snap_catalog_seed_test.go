package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/inference/internal/snapcatalog"
)

type fetcherFunc func(context.Context) ([]byte, error)

func (f fetcherFunc) Fetch(ctx context.Context) ([]byte, error) {
	return f(ctx)
}

func TestSnapCatalogSeedWritesDocument(t *testing.T) {
	fetchedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fetcher := fetcherFunc(func(context.Context) ([]byte, error) {
		return []byte(`<table>
			<tr><th>Snap</th><th>Model</th><th>Repository</th></tr>
			<tr><td>gemma4</td><td>Gemma 4</td><td><a href="https://github.com/canonical/gemma4-snap">canonical/gemma4-snap</a></td></tr>
		</table>`), nil
	})
	path := filepath.Join(t.TempDir(), "seed.json")
	cmd := snapCatalogSeed(fetcher, func() time.Time { return fetchedAt })
	cmd.SetArgs([]string{path})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	document, err := snapcatalog.DecodeDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if !document.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("FetchedAt=%v, want %v", document.FetchedAt, fetchedAt)
	}
	if len(document.Entries) != 1 || document.Entries[0].SnapName != "gemma4" {
		t.Fatalf("Entries=%v", document.Entries)
	}
}
