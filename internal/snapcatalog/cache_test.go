package snapcatalog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testDocument(at time.Time) Document {
	return Document{
		SchemaVersion: SchemaVersion,
		FetchedAt:     at,
		Entries: []Entry{
			{SnapName: "gemma4", Model: "Gemma 4", Repository: "canonical/gemma4-snap"},
			{SnapName: "qwen3", Model: "Qwen 3", Repository: "canonical/qwen3-snap"},
		},
	}
}

func TestCacheSaveLoadRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cache := Cache{Path: filepath.Join(t.TempDir(), "nested", "snap-catalog.json")}

	if err := cache.Save(testDocument(now)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := cache.Load(now)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Entries) != 2 || got.Entries[0].SnapName != "gemma4" {
		t.Fatalf("got %+v", got)
	}
}

func TestCacheSaveInvalidDocumentPreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap-catalog.json")
	if err := os.WriteFile(path, []byte("last known good"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := Cache{Path: path}
	if err := cache.Save(Document{}); err == nil {
		t.Fatal("expected invalid document error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "last known good" {
		t.Fatalf("existing cache was changed: %q", got)
	}
}

func TestCacheRejectsFutureTimestamp(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cache := Cache{Path: filepath.Join(t.TempDir(), "snap-catalog.json")}
	if err := cache.Save(testDocument(now.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(now); err == nil {
		t.Fatal("expected future timestamp error")
	}
}

func TestDefaultCachePath(t *testing.T) {
	t.Setenv("SNAP_USER_COMMON", "/tmp/inference-common")
	path, err := DefaultCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/inference-common/snap-catalog.json" {
		t.Fatalf("got %q", path)
	}
}
