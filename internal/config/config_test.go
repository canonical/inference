package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

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
	for _, enabled := range []bool{true, false} {
		want := map[string]Provider{"remote": {Enabled: enabled}}
		if err := writeConfig(c.Path, &Config{Providers: want}); err != nil {
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
	want := &Config{
		Providers: map[string]Provider{
			"local":  {Enabled: true},
			"remote": {Enabled: false},
		},
	}

	if err := writeConfig(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readConfig(path)
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
	if err := writeConfig(path, &Config{Providers: map[string]Provider{"a": {Enabled: true}}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readConfig(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.Providers["a"].Enabled {
		t.Fatalf("got %+v, want provider a enabled", got.Providers)
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

func TestAddProviderValidatesName(t *testing.T) {
	for _, tt := range []struct {
		name    string
		wantErr bool
	}{
		{name: "provider.example-v1"},
		{name: "", wantErr: true},
		{name: ".", wantErr: true},
		{name: "..", wantErr: true},
		{name: "provider..name", wantErr: true},
		{name: "../victim", wantErr: true},
		{name: "nested/provider", wantErr: true},
		{name: `nested\provider`, wantErr: true},
		{name: "provider\x00name", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
			err := c.AddProvider(tt.name, true)
			if (err != nil) != tt.wantErr {
				t.Fatalf("AddProvider error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestWriteCleansUpTemporaryFileOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := writeConfig(path, &Config{}); err == nil {
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

func TestLockAndReadConfigReleasesLockOnReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readConfigLocked(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	file, err := os.OpenFile(path+".lock", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("closing lock file: %v", err)
		}
	})
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("lock was not released after read error: %v", err)
	}
}

func TestProviderJSONContract(t *testing.T) {
	const data = `{"providers":{"gemma4":{"enabled":true},"qwen3-8":{"enabled":false}}}`
	var cfg Config
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatal(err)
	}
	want := map[string]Provider{"gemma4": {Enabled: true}, "qwen3-8": {Enabled: false}}
	if !reflect.DeepEqual(cfg.Providers, want) {
		t.Fatalf("got %+v, want %+v", cfg.Providers, want)
	}
	got, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Fatalf("got JSON %s, want %s", got, data)
	}
}

func TestRemoveProvider(t *testing.T) {
	c := &ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	if err := c.AddProvider("gemma4", true); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveProvider("gemma4"); err != nil {
		t.Fatal(err)
	}
	if exists, err := c.ProviderExists("gemma4"); err != nil || exists {
		t.Fatalf("ProviderExists = %t, %v; want false, nil", exists, err)
	}
	if err := c.RemoveProvider("gemma4"); err == nil {
		t.Fatal("expected error removing missing provider")
	}
}

func TestAddProviderRejectsDuplicate(t *testing.T) {
	c := &ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	if err := c.AddProvider("gemma4", true); err != nil {
		t.Fatal(err)
	}
	if err := c.AddProvider("gemma4", true); err == nil {
		t.Fatal("expected error for duplicate provider")
	}
}
