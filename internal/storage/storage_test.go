package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func TestNew(t *testing.T) {
	snapData := t.TempDir()
	customPath := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv("SNAP_DATA", snapData)

	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{name: "default", want: filepath.Join(snapData, "storage.json")},
		{name: "custom", path: customPath, want: customPath},
		{name: "expanded custom", path: "$SNAP_DATA/custom.json", want: filepath.Join(snapData, "custom.json")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(filePathEnvVar, tt.path)
			if got := New().Path; got != tt.want {
				t.Fatalf("Path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStorageGetProvidersReloadsFile(t *testing.T) {
	c := &Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	for _, enabled := range []bool{true, false} {
		want := map[string]Provider{"remote": {Enabled: enabled}}
		if err := writeDocument(c.Path, &Document{Providers: want}); err != nil {
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
	path := filepath.Join(t.TempDir(), "storage.json")
	want := &Document{
		Providers: map[string]Provider{
			"local":  {Enabled: true},
			"remote": {Enabled: false},
		},
	}

	if err := writeDocument(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readDocument(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestWriteReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDocument(path, &Document{Providers: map[string]Provider{"a": {Enabled: true}}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readDocument(path)
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
		t.Fatalf("expected only the storage file, found %d entries", len(entries))
	}
}

func TestStorageMissingFileReturnsEmptyProviders(t *testing.T) {
	c := &Storage{Path: filepath.Join(t.TempDir(), "missing.json")}
	providers, err := c.GetProviders()
	if err != nil {
		t.Fatalf("expected no error for missing storage file, got %v", err)
	}
	if providers == nil || len(providers) != 0 {
		t.Fatalf("expected non-nil empty providers, got %v", providers)
	}
}

func TestSetProviderEnabledValidatesName(t *testing.T) {
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
			c := &Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
			_, err := c.SetProviderEnabled(tt.name, true)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SetProviderEnabled error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestWriteCleansUpTemporaryFileOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := writeDocument(path, &Document{}); err == nil {
		t.Fatal("expected error when replacing a directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "storage.json" || !entries[0].IsDir() {
		t.Fatalf("expected only the original directory, got %v", entries)
	}
}

func TestReadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Storage{Path: path}
	if _, err := c.GetProviders(); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLockAndReadDocumentReleasesLockOnReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readDocumentLocked(path); err == nil {
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
	var doc Document
	if err := json.Unmarshal([]byte(data), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]Provider{"gemma4": {Enabled: true}, "qwen3-8": {Enabled: false}}
	if !reflect.DeepEqual(doc.Providers, want) {
		t.Fatalf("got %+v, want %+v", doc.Providers, want)
	}
	got, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Fatalf("got JSON %s, want %s", got, data)
	}
}

func TestEnsureProviders(t *testing.T) {
	c := &Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	if _, err := c.SetProviderEnabled("existing", false); err != nil {
		t.Fatal(err)
	}

	got, err := c.EnsureProviders([]string{"existing", "new"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Provider{"existing": {Enabled: false}, "new": {Enabled: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	persisted, err := c.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("persisted %+v, want %+v", persisted, want)
	}
}

func TestEnsureProvidersDoesNotRewriteCompleteStorage(t *testing.T) {
	c := &Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	if _, err := c.EnsureProviders([]string{"gemma4"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(c.Path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.EnsureProviders([]string{"gemma4"}); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("storage file was rewritten although no provider was missing")
	}
}

func TestEnsureProvidersRejectsInvalidNames(t *testing.T) {
	c := &Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	if _, err := c.EnsureProviders([]string{"ok", "../bad"}); err == nil {
		t.Fatal("expected error for invalid name")
	}
	providers, err := c.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 0 {
		t.Fatalf("expected no providers written, got %+v", providers)
	}
}

func TestSetProviderEnabled(t *testing.T) {
	c := &Storage{Path: filepath.Join(t.TempDir(), "storage.json")}

	for _, step := range []struct {
		enabled     bool
		wantChanged bool
	}{
		{enabled: true, wantChanged: false},
		{enabled: false, wantChanged: true},
		{enabled: false, wantChanged: false},
		{enabled: true, wantChanged: true},
	} {
		changed, err := c.SetProviderEnabled("gemma4", step.enabled)
		if err != nil {
			t.Fatal(err)
		}
		if changed != step.wantChanged {
			t.Fatalf("SetProviderEnabled(%t) changed = %t, want %t", step.enabled, changed, step.wantChanged)
		}
		providers, err := c.GetProviders()
		if err != nil {
			t.Fatal(err)
		}
		if providers["gemma4"].Enabled != step.enabled {
			t.Fatalf("enabled = %t, want %t", providers["gemma4"].Enabled, step.enabled)
		}
	}
}

func TestRemoveProvider(t *testing.T) {
	c := &Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	if _, err := c.EnsureProviders([]string{"gemma4"}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveProvider("gemma4"); err != nil {
		t.Fatal(err)
	}
	if exists, err := c.ProviderExists("gemma4"); err != nil || exists {
		t.Fatalf("ProviderExists = %t, %v; want false, nil", exists, err)
	}
	if err := c.RemoveProvider("gemma4"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("got %v, want ErrProviderNotFound", err)
	}
}

func TestAddProvider(t *testing.T) {
	c := &Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	if err := c.AddProvider("gemma4", false); err != nil {
		t.Fatal(err)
	}
	providers, err := c.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]Provider{"gemma4": {Enabled: false}}); !reflect.DeepEqual(providers, want) {
		t.Fatalf("got %+v, want %+v", providers, want)
	}
	if err := c.AddProvider("gemma4", true); err == nil {
		t.Fatal("expected error for duplicate provider")
	}
	if err := c.AddProvider("../bad", true); err == nil {
		t.Fatal("expected error for invalid provider name")
	}
}
