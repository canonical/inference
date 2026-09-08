package providers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
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

func TestDefaultShareProvidersPath(t *testing.T) {
	t.Run("override", func(t *testing.T) {
		t.Setenv(ShareProvidersEnvVar, "/override/providers")
		t.Setenv("SNAP", "/snap/inference/current")
		if got := DefaultShareProvidersPath(); got != "/override/providers" {
			t.Fatalf("got %q, want override path", got)
		}
	})

	t.Run("snap default", func(t *testing.T) {
		t.Setenv(ShareProvidersEnvVar, "")
		t.Setenv("SNAP", "/snap/inference/current")
		want := "/snap/inference/current/share/providers"
		if got := DefaultShareProvidersPath(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("not configured", func(t *testing.T) {
		t.Setenv(ShareProvidersEnvVar, "")
		t.Setenv("SNAP", "")
		if got := DefaultShareProvidersPath(); got != "" {
			t.Fatalf("got %q, want empty path", got)
		}
	})
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
		Type:       TypeInferenceSnap,
		State:      StateUnknown,
		Connection: ConnectionConnected,
		BaseURL:    "https://user:password@example.com/v1/?region=eu#backend",
	}
	if len(got) != 1 || got[0] != want {
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
	catalog := writeCatalog(t, `[
		{"snap":"published","model_name":"Published","full_name":"canonical/published","html_url":"https://example.com/published"},
		{"snap":"disconnected","model_name":"Disconnected","full_name":"canonical/disconnected","html_url":"https://example.com/disconnected"}
	]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "published-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=published\n")
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8081/v1\nSNAP_NAME=custom\n")
	client := newSnapdServer(t, map[string]string{
		"published": snapd.SnapStatusActive,
		"custom":    snapd.SnapStatusInstalled,
	})

	result, err := List(context.Background(), catalog, client, root, ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []Provider{
		{
			Name:       "custom",
			Type:       TypeInferenceSnap,
			State:      StateDisabled,
			Connection: ConnectionConnected,
			BaseURL:    "http://localhost:8081/v1",
		},
		{
			Name:       "disconnected",
			Type:       TypeInferenceSnap,
			State:      StateNotInstalled,
			Connection: ConnectionNotConnected,
		},
		{
			Name:       "published",
			Type:       TypeInferenceSnap,
			State:      StateEnabled,
			Connection: ConnectionConnected,
			BaseURL:    "http://localhost:8080/v1",
		},
	}
	if len(result) != len(want) {
		t.Fatalf("got %+v, want %+v", result, want)
	}
	for i := range want {
		if result[i] != want[i] {
			t.Fatalf("provider %d: got %+v, want %+v", i, result[i], want[i])
		}
	}
}

func TestListFailsWhenSnapStatusCannotBeRead(t *testing.T) {
	catalog := writeCatalog(t, `[]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=custom\n")
	client := &snapd.Client{Socket: filepath.Join(t.TempDir(), "missing.socket")}

	_, err := List(
		context.Background(),
		catalog,
		client,
		root,
		ListOptions{InstalledOnly: true},
	)
	if err == nil {
		t.Fatal("got nil error, want snapd error")
	}
}

func TestListFailsWhenCatalogFails(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=custom\n")
	client := newSnapdServer(t, map[string]string{"custom": snapd.SnapStatusActive})

	_, err := List(
		context.Background(),
		&snapcatalog.Reader{},
		client,
		root,
		ListOptions{},
	)
	if err == nil {
		t.Fatal("got nil error, want catalog error")
	}
}

func TestListFailsWhenConnectedSourceFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog := writeCatalog(t, `[]`)
	_, err := List(
		context.Background(),
		catalog,
		newSnapdServer(t, nil),
		root,
		ListOptions{},
	)
	if err == nil {
		t.Fatal("got nil error, want connected source error")
	}
}
