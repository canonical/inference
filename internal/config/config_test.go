package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProviderJSONContract(t *testing.T) {
	const data = `{"enabled":true,"backend":"proxy","base-url":"https://api.openai.com/v1","api":"openai","auth-scheme":"api-key"}`
	var provider Provider
	if err := json.Unmarshal([]byte(data), &provider); err != nil {
		t.Fatal(err)
	}
	if provider.BaseURL != "https://api.openai.com/v1" || provider.API == nil || *provider.API != "openai" || provider.AuthScheme != AuthorizationAPIKey {
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

func TestNewConfigReader(t *testing.T) {
	snapData := t.TempDir()
	customPath := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv("SNAP_DATA", snapData)

	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{name: "default", want: filepath.Join(snapData, "config.json")},
		{name: "custom", path: customPath, want: customPath},
		{name: "expanded custom", path: "$SNAP_DATA/custom.json", want: filepath.Join(snapData, "custom.json")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(configFilePathEnvVar, tt.path)
			if got := NewConfigReader().Path; got != tt.want {
				t.Fatalf("Path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigReaderGetProvidersReloadsFile(t *testing.T) {
	c := &ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	for _, baseURL := range []string{"https://example.com/v1", "https://updated.example.com/v1"} {
		want := map[string]Provider{
			"remote": {Enabled: true, Backend: BackendProxy, BaseURL: baseURL},
		}
		if err := write(c.Path, &Config{Version: 1, Providers: want}); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := c.GetProviders()
		if err != nil {
			t.Fatalf("GetProviders: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	snap := "gemma3"
	want := &Config{
		Version:           1,
		DiscoverProviders: true,
		Providers: map[string]Provider{
			"local":  {Backend: BackendSnap, BaseURL: "http://localhost:8326/v1", Snap: &snap, AuthScheme: AuthorizationNone},
			"remote": {Backend: BackendProxy, BaseURL: "https://api.openai.com/v1", AuthScheme: AuthorizationAPIKey},
		},
	}

	if err := write(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
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
	if err := write(path, &Config{Version: 2}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
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

func TestConfigReaderMissingFileReturnsEmptyProviders(t *testing.T) {
	c := &ConfigReader{Path: filepath.Join(t.TempDir(), "missing.json")}
	providers, err := c.GetProviders()
	if err != nil {
		t.Fatalf("expected no error for missing config file, got %v", err)
	}
	if providers == nil || len(providers) != 0 {
		t.Fatalf("expected non-nil empty providers, got %v", providers)
	}
}

func TestWriteCleansUpTemporaryFileOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := write(path, &Config{Version: 1}); err == nil {
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
	c := &ConfigReader{Path: path}
	if _, err := c.GetProviders(); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSetProviderKeyCanBeReadAfterConfigReload(t *testing.T) {
	snapData := t.TempDir()
	t.Setenv("SNAP_DATA", snapData)
	t.Setenv(configFilePathEnvVar, "")
	c := NewConfigReader()
	cfg := newConfig()
	cfg.Providers["openai"] = Provider{Backend: BackendProxy, AuthScheme: AuthorizationNone}
	if err := write(c.Path, &cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	key := "test-api-key"

	if err := c.SetProviderKey("openai", &key); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}

	reloaded := NewConfigReader()
	got, err := reloaded.GetProviderKey("openai")
	if err != nil {
		t.Fatalf("GetProviderKey: %v", err)
	}
	if got == nil || *got != key {
		t.Fatalf("GetProviderKey = %v, want %q", got, key)
	}
}

func TestAddOpenAIProviderWritesReadableKey(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  *string
	}{
		{name: "with key", key: new("test-api-key")},
		{name: "without key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SNAP_DATA", t.TempDir())
			t.Setenv(configFilePathEnvVar, "")
			c := NewConfigReader()
			if err := c.AddOpenAIProvider("openai", "https://api.openai.com/v1", tt.key); err != nil {
				t.Fatalf("AddOpenAIProvider: %v", err)
			}
			reloaded := NewConfigReader()
			got, err := reloaded.GetProviderKey("openai")
			if err != nil {
				t.Fatalf("GetProviderKey: %v", err)
			}
			if !reflect.DeepEqual(got, tt.key) {
				t.Fatalf("GetProviderKey = %v, want %v", got, tt.key)
			}
			providers, err := reloaded.GetProviders()
			if err != nil {
				t.Fatalf("GetProviders: %v", err)
			}
			want := Provider{
				Enabled:    true,
				Backend:    BackendProxy,
				BaseURL:    "https://api.openai.com/v1",
				API:        new("openai"),
				AuthScheme: AuthorizationNone,
			}
			if tt.key != nil {
				want.AuthScheme = AuthorizationAPIKey
			}
			if !reflect.DeepEqual(providers, map[string]Provider{"openai": want}) {
				t.Fatalf("got providers %+v, want openai: %+v", providers, want)
			}
		})
	}
}

func TestProviderAPIKey(t *testing.T) {
	t.Setenv("SNAP_DATA", t.TempDir())
	t.Setenv(configFilePathEnvVar, "")
	c := NewConfigReader()
	keyDirectory := filepath.Join(filepath.Dir(c.Path), ".keys")
	if err := os.MkdirAll(keyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(keyDirectory, "with-key.key")
	if err := os.WriteFile(keyFile, []byte("test-api-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := newConfig()
	cfg.Providers["with-key"] = Provider{Backend: BackendProxy, AuthScheme: AuthorizationAPIKey}
	cfg.Providers["without-key"] = Provider{Backend: BackendProxy, AuthScheme: AuthorizationNone}
	cfg.Providers["missing-key"] = Provider{Backend: BackendProxy, AuthScheme: AuthorizationAPIKey}
	if err := write(c.Path, &cfg); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, tt := range []struct {
		provider string
		want     string
		wantErr  bool
	}{
		{provider: "with-key", want: "test-api-key"},
		{provider: "without-key"},
		{provider: "unconfigured"},
		{provider: "missing-key", wantErr: true},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			got, err := c.ProviderAPIKey(tt.provider)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ProviderAPIKey error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ProviderAPIKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetProviderKeyClearsKey(t *testing.T) {
	t.Setenv("SNAP_DATA", t.TempDir())
	t.Setenv(configFilePathEnvVar, "")
	c := NewConfigReader()
	cfg := newConfig()
	cfg.Providers["openai"] = Provider{Backend: BackendProxy, AuthScheme: AuthorizationNone}
	if err := write(c.Path, &cfg); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := c.SetProviderKey("openai", nil); err != nil {
		t.Fatalf("SetProviderKey without existing key: %v", err)
	}

	key := "test-api-key"
	if err := c.SetProviderKey("openai", &key); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	providers, err := c.GetProviders()
	if err != nil {
		t.Fatalf("GetProviders: %v", err)
	}
	keyFile := filepath.Join(os.Getenv("SNAP_DATA"), ".keys", "openai.key")
	if providers["openai"].AuthScheme != AuthorizationAPIKey {
		t.Fatalf("AuthScheme = %q, want %q", providers["openai"].AuthScheme, AuthorizationAPIKey)
	}
	if err := c.SetProviderKey("openai", nil); err != nil {
		t.Fatalf("SetProviderKey to clear key: %v", err)
	}
	providers, err = c.GetProviders()
	if err != nil {
		t.Fatalf("GetProviders: %v", err)
	}
	if providers["openai"].AuthScheme != AuthorizationNone {
		t.Fatalf("AuthScheme = %q, want %q", providers["openai"].AuthScheme, AuthorizationNone)
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatalf("expected key file to be removed, stat error = %v", err)
	}
}
