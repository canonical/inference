package providers

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/canonical/inference/internal/storage"
)

func writeProviderEnv(t *testing.T, root, name, contents string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating provider directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provider.env"), []byte(contents), 0o644); err != nil {
		t.Fatalf("writing provider.env: %v", err)
	}
}

func TestConnectedSnapProviders(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "custom", `
		# custom provider
		OPENAI_BASE_URL="https://user:password@example.com/v1/?region=eu#backend"
		SNAP_NAME=custom
	`)
	if err := os.Mkdir(filepath.Join(root, "without-env"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	want := Provider{
		Name:       "custom",
		Backend:    BackendSnap,
		State:      StateUnknown,
		Connection: ConnectionConnected,
		BaseURL:    "https://user:password@example.com/v1/?region=eu#backend",
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("got %+v, want [%+v]", got, want)
	}
}

func TestConnectedSnapProvidersMissingRoot(t *testing.T) {
	got, err := ConnectedSnapProviders(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got providers=%v, want empty", got)
	}
}

func TestConnectedSnapProvidersRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name string
		envs map[string]string
		want string
	}{
		{
			name: "invalid URL",
			envs: map[string]string{"invalid": "OPENAI_BASE_URL=ftp://example.com\nSNAP_NAME=invalid\n"},
			want: "invalid",
		},
		{
			name: "missing snap name",
			envs: map[string]string{"custom": "OPENAI_BASE_URL=http://localhost:8081/v1\n"},
			want: "SNAP_NAME is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, contents := range tt.envs {
				writeProviderEnv(t, root, name, contents)
			}

			_, err := ConnectedSnapProviders(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got error %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestConnectedSnapProvidersRejectsDuplicateSnapName(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "a", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=same\n")
	writeProviderEnv(t, root, "b", "OPENAI_BASE_URL=http://localhost:8081/v1\nSNAP_NAME=same\n")

	_, err := ConnectedSnapProviders(root)
	if err == nil || !strings.Contains(err.Error(), `duplicate SNAP_NAME "same"`) {
		t.Fatalf("got error %v, want duplicate SNAP_NAME error", err)
	}
}

func TestListMergesCatalogAndConnectedProviders(t *testing.T) {
	catalog := snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"published","model_name":"Published","repo_url":"https://example.com/published"},
		{"snap":"disconnected","model_name":"Disconnected","repo_url":"https://example.com/disconnected"}
	]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "published-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=published\n")
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8081/v1\nSNAP_NAME=custom\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{
		"published": snapd.SnapStatusActive,
		"custom":    snapd.SnapStatusInstalled,
	})

	result, err := NewService(catalog, client, root, nil).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []Provider{
		{
			Name:       "custom",
			Backend:    BackendSnap,
			State:      StateDisabled,
			Connection: ConnectionConnected,
			BaseURL:    "http://localhost:8081/v1",
		},
		{
			Name:       "disconnected",
			Backend:    BackendSnap,
			State:      StateNotInstalled,
			Connection: ConnectionNotConnected,
		},
		{
			Name:       "published",
			Backend:    BackendSnap,
			State:      StateEnabled,
			Connection: ConnectionConnected,
			BaseURL:    "http://localhost:8080/v1",
		},
	}
	if len(result) != len(want) {
		t.Fatalf("got %+v, want %+v", result, want)
	}
	for i := range want {
		if !reflect.DeepEqual(result[i], want[i]) {
			t.Fatalf("provider %d: got %+v, want %+v", i, result[i], want[i])
		}
	}
}

func TestListFailsWhenSnapStatusCannotBeRead(t *testing.T) {
	catalog := snapcatalog.WriteFakeCatalog(t, `[]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=custom\n")
	client := &snapd.Client{Socket: filepath.Join(t.TempDir(), "missing.socket")}

	_, err := NewService(catalog, client, root, nil).ListInstalled(context.Background())
	if err == nil {
		t.Fatal("got nil error, want snapd error")
	}
}

func TestListFailsWhenCatalogFails(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=custom\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{"custom": snapd.SnapStatusActive})

	_, err := NewService(snapcatalog.WriteFakeCatalog(t, "not json"), client, root, nil).List(context.Background())
	if err == nil {
		t.Fatal("got nil error, want catalog error")
	}
}

func TestListFailsWhenConnectedSourceFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog := snapcatalog.WriteFakeCatalog(t, `[]`)
	client, _ := snapd.NewFakeServer(t, nil)
	_, err := NewService(catalog, client, root, nil).List(context.Background())
	if err == nil {
		t.Fatal("got nil error, want connected source error")
	}
}

func TestListSyncsConnectedProvidersIntoStorage(t *testing.T) {
	catalog := snapcatalog.WriteFakeCatalog(t, `[]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "a-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=a\n")
	writeProviderEnv(t, root, "b-mount", "OPENAI_BASE_URL=http://localhost:8081/v1\nSNAP_NAME=b\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{
		"a": snapd.SnapStatusActive,
		"b": snapd.SnapStatusActive,
	})
	want := map[string]storage.Provider{"a": {Enabled: false}, "b": {Enabled: true}}
	storage := &storage.Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	if _, err := storage.SetProviderEnabled("a", false); err != nil {
		t.Fatal(err)
	}

	result, err := NewSyncingService(catalog, client, root, storage).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	states := map[string]LifecycleState{}
	for _, provider := range result {
		states[provider.Name] = provider.State
	}
	if states["a"] != StateDisabled || states["b"] != StateEnabled {
		t.Fatalf("got states %v, want a disabled and b enabled", states)
	}
	persisted, err := storage.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("storage providers %+v, want %+v", persisted, want)
	}
}

func TestListReadsStorageOnEveryCall(t *testing.T) {
	catalog := snapcatalog.WriteFakeCatalog(t, `[]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "a-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=a\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{"a": snapd.SnapStatusActive})
	storage := &storage.Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	service := NewService(catalog, client, root, storage)

	for _, enabled := range []bool{true, false, true} {
		if _, err := storage.SetProviderEnabled("a", enabled); err != nil {
			t.Fatal(err)
		}
		provider, err := service.Find(context.Background(), "a")
		if err != nil {
			t.Fatal(err)
		}
		if got := provider.State == StateEnabled; got != enabled {
			t.Fatalf("enabled = %t, want %t", got, enabled)
		}
	}
}

func TestListKeepsDisabledProvidersInstalled(t *testing.T) {
	catalog := snapcatalog.WriteFakeCatalog(t, `[{"snap":"a","model_name":"A","repo_url":"https://example.com/a"}]`)
	client, _ := snapd.NewFakeServer(t, map[string]string{"a": snapd.SnapStatusActive})
	storage := &storage.Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	if _, err := storage.SetProviderEnabled("a", false); err != nil {
		t.Fatal(err)
	}

	result, err := NewService(catalog, client, "", storage).ListInstalled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].State != StateDisabled {
		t.Fatalf("got %+v, want one disabled provider", result)
	}
}

func TestListWithoutStorageSyncDoesNotWriteStorage(t *testing.T) {
	catalog := snapcatalog.WriteFakeCatalog(t, `[]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "a-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=a\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{"a": snapd.SnapStatusActive})
	storage := &storage.Storage{Path: filepath.Join(t.TempDir(), "storage.json")}

	result, err := NewService(catalog, client, root, storage).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 1 || result[0].State != StateEnabled {
		t.Fatalf("got %+v, want one enabled provider", result)
	}
	if _, err := os.Stat(storage.Path); !os.IsNotExist(err) {
		t.Fatalf("storage file should not be created, stat error = %v", err)
	}
}
