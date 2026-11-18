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

func TestConnectedSnapProvidersReportsMissingBaseURL(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "socket-only", "SNAP_NAME=socket-only\nUNIX_SOCKET=x.sock\n")

	got, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	want := Provider{
		Name:       "socket-only",
		Backend:    BackendSnap,
		State:      StateUnknown,
		Connection: ConnectionConnected,
	}
	assertSingleProviderError(t, got, want, "OPENAI_BASE_URL is empty")
}

func TestConnectedSnapProvidersReportsInvalidBaseURL(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "invalid", "OPENAI_BASE_URL=ftp://example.com\nSNAP_NAME=invalid\n")

	got, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	want := Provider{Name: "invalid", Backend: BackendSnap, State: StateUnknown, Connection: ConnectionConnected}
	assertSingleProviderError(t, got, want, "scheme must be http or https")
}

func assertSingleProviderError(t *testing.T, got []Provider, want Provider, wantError string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %+v, want one provider", got)
	}
	gotErr := got[0].Err
	got[0].Err = nil
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("got %+v, want provider %+v", got[0], want)
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), wantError) {
		t.Fatalf("got error %v, want one containing %q", gotErr, wantError)
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

func TestConnectedSnapProvidersReportsInvalidEntries(t *testing.T) {
	tests := []struct {
		name string
		envs map[string]string
		want Provider
		err  string
	}{
		{
			name: "missing snap name",
			envs: map[string]string{"custom": "OPENAI_BASE_URL=http://localhost:8081/v1\n"},
			want: Provider{Name: "custom", Backend: BackendSnap, State: StateUnknown, Connection: ConnectionConnected},
			err:  "SNAP_NAME is empty",
		},
		{
			name: "unreadable env file",
			envs: map[string]string{"custom": "SNAP_NAME='unterminated\n"},
			want: Provider{Name: "custom", Backend: BackendSnap, State: StateUnknown, Connection: ConnectionConnected},
			err:  `reading connected provider "custom"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, contents := range tt.envs {
				writeProviderEnv(t, root, name, contents)
			}

			got, err := ConnectedSnapProviders(root)
			if err != nil {
				t.Fatalf("ConnectedSnapProviders: %v", err)
			}
			assertSingleProviderError(t, got, tt.want, tt.err)
		})
	}
}

func TestConnectedSnapProvidersReportsDuplicateSnapName(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "a", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=same\n")
	writeProviderEnv(t, root, "b", "OPENAI_BASE_URL=http://localhost:8081/v1\nSNAP_NAME=same\n")

	got, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	want := Provider{Name: "same", Backend: BackendSnap, State: StateUnknown, Connection: ConnectionConnected}
	assertSingleProviderError(t, got, want, `duplicate provider name "same"`)
}

func TestConnectedSnapProvidersKeepsValidProvidersNextToInvalidOnes(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "broken", "SNAP_NAME=broken\nOPENAI_BASE_URL=not a url\n")
	writeProviderEnv(t, root, "healthy", "SNAP_NAME=healthy\nOPENAI_BASE_URL=http://localhost:8080/v1\n")

	got, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	if len(got) != 2 || got[0].Name != "broken" || got[0].Err == nil || got[1].Name != "healthy" || got[1].Err != nil {
		t.Fatalf("got %+v, want broken provider with an error followed by healthy provider", got)
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

	result, err := ListAll(context.Background(), catalog, client, root)
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
		if !reflect.DeepEqual(result[i], want[i]) || result[i].Err != nil {
			t.Fatalf("provider %d: got %+v, want %+v", i, result[i], want[i])
		}
	}
}

func TestListReportsConnectedProviderWithoutBaseURL(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "socket-mount", "SNAP_NAME=socket-only\nUNIX_SOCKET=x.sock\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{"socket-only": snapd.SnapStatusActive})

	got, err := ListInstalled(
		context.Background(),
		snapcatalog.WriteFakeCatalog(t, `[]`),
		client,
		root,
	)
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	want := Provider{
		Name:       "socket-only",
		Backend:    BackendSnap,
		State:      StateUnknown,
		Connection: ConnectionConnected,
	}
	assertSingleProviderError(t, got, want, "OPENAI_BASE_URL is empty")
}

func TestListFailsWhenSnapStatusCannotBeRead(t *testing.T) {
	catalog := snapcatalog.WriteFakeCatalog(t, `[]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=custom\n")
	client := &snapd.Client{Socket: filepath.Join(t.TempDir(), "missing.socket")}

	_, err := ListInstalled(
		context.Background(),
		catalog,
		client,
		root,
	)
	if err == nil {
		t.Fatal("got nil error, want snapd error")
	}
}

func TestListFailsWhenCatalogFails(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=custom\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{"custom": snapd.SnapStatusActive})

	_, err := ListAll(
		context.Background(),
		snapcatalog.WriteFakeCatalog(t, "not json"),
		client,
		root,
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

	catalog := snapcatalog.WriteFakeCatalog(t, `[]`)
	client, _ := snapd.NewFakeServer(t, nil)
	_, err := ListAll(
		context.Background(),
		catalog,
		client,
		root,
	)
	if err == nil {
		t.Fatal("got nil error, want connected source error")
	}
}
