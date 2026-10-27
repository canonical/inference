package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteThenReadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	snap := "gemma3"
	keyFile := "/var/snap/inference/common/openai.key"
	want := &Config{
		Version:           1,
		DiscoverProviders: true,
		Providers: map[string]Provider{
			"local":  {Backend: BackendSnap, BaseUrl: "http://localhost:8326/v1", Snap: &snap},
			"remote": {Backend: BackendProxy, BaseUrl: "https://api.openai.com/v1", KeyFile: &keyFile},
		},
	}

	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestWriteReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, &Config{Version: 2}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("got version %d, want 2", got.Version)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the config file, found %d entries", len(entries))
	}
}

func TestMissingConfigFileReturnsNewConfig(t *testing.T) {
	c, err := Read(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("expected no error for missing config file, got %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil config for missing config file")
	}
}

func TestReadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
