package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProviderJSONContract(t *testing.T) {
	const data = `{"enabled":true,"backend":"proxy","base-url":"https://api.openai.com/v1","api":"openai"}`
	var provider Provider
	if err := json.Unmarshal([]byte(data), &provider); err != nil {
		t.Fatal(err)
	}
	if provider.BaseURL != "https://api.openai.com/v1" || provider.API == nil || *provider.API != "openai" {
		t.Fatalf("unexpected provider connection settings: %+v", provider)
	}
	got, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Fatalf("got JSON %s, want %s", got, data)
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	snap := "gemma3"
	keyFile := "/var/snap/inference/common/openai.key"
	want := &Config{
		Version:           1,
		DiscoverProviders: true,
		Providers: map[string]Provider{
			"local":  {Backend: BackendSnap, BaseURL: "http://localhost:8326/v1", Snap: &snap},
			"remote": {Backend: BackendProxy, BaseURL: "https://api.openai.com/v1", KeyFile: &keyFile},
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

func TestWriteCleansUpTemporaryFileOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := Write(path, &Config{Version: 1}); err == nil {
		t.Fatal("expected error when replacing a directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" || !entries[0].IsDir() {
		t.Fatalf("expected only the original directory, got %v", entries)
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

func TestSetProviderKeyCanBeReadAfterConfigReload(t *testing.T) {
	snapData := t.TempDir()
	t.Setenv("SNAP_DATA", snapData)
	c := NewConfig()
	c.Providers["openai"] = Provider{Backend: BackendProxy}
	key := "test-api-key"

	if err := c.SetProviderKey("openai", &key); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}

	configPath := filepath.Join(snapData, "config.json")
	if err := Write(configPath, &c); err != nil {
		t.Fatalf("Write: %v", err)
	}
	reloaded, err := Read(configPath)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, err := reloaded.GetProviderKey("openai")
	if err != nil {
		t.Fatalf("GetProviderKey: %v", err)
	}
	if got == nil || *got != key {
		t.Fatalf("GetProviderKey = %v, want %q", got, key)
	}
}

func TestAddOpenAIProviderWritesReadableKey(t *testing.T) {
	t.Setenv("SNAP_DATA", t.TempDir())
	c := NewConfig()
	key := "test-api-key"

	if err := c.AddOpenAIProvider("openai", "https://api.openai.com/v1", &key); err != nil {
		t.Fatalf("AddOpenAIProvider: %v", err)
	}
	got, err := c.GetProviderKey("openai")
	if err != nil {
		t.Fatalf("GetProviderKey: %v", err)
	}
	if got == nil || *got != key {
		t.Fatalf("GetProviderKey = %v, want %q", got, key)
	}
}

func TestSetProviderKeyClearsKey(t *testing.T) {
	t.Setenv("SNAP_DATA", t.TempDir())
	c := NewConfig()
	c.Providers["openai"] = Provider{Backend: BackendProxy}

	if err := c.SetProviderKey("openai", nil); err != nil {
		t.Fatalf("SetProviderKey without existing key: %v", err)
	}

	key := "test-api-key"
	if err := c.SetProviderKey("openai", &key); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	keyFile := *c.Providers["openai"].KeyFile
	if err := c.SetProviderKey("openai", nil); err != nil {
		t.Fatalf("SetProviderKey to clear key: %v", err)
	}
	if c.Providers["openai"].KeyFile != nil {
		t.Fatal("expected KeyFile to be cleared")
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatalf("expected key file to be removed, stat error = %v", err)
	}
}
