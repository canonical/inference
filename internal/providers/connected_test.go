package providers

import (
	"context"
	"fmt"
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

	got, issues, err := ConnectedSnapProviders(root)
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
	if len(issues) != 0 {
		t.Fatalf("got issues %+v, want none", issues)
	}
}

func TestConnectedSnapProvidersMissingRoot(t *testing.T) {
	got, issues, err := ConnectedSnapProviders(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	if len(got) != 0 || len(issues) != 0 {
		t.Fatalf("got providers=%v issues=%v, want empty", got, issues)
	}
}

func TestConnectedSnapProvidersRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name string
		env  *string
		want string
	}{
		{
			name: "missing provider.env",
			want: "Cannot use shared provider: missing provider.env in %[1]s/custom/",
		},
		{
			name: "missing base URL",
			env:  ptr("SNAP_NAME=custom\nUNIX_SOCKET=x.sock\n"),
			want: "Cannot use shared provider: missing OPENAI_BASE_URL in %[1]s/custom/provider.env",
		},
		{
			name: "empty base URL",
			env:  ptr("SNAP_NAME=custom\nOPENAI_BASE_URL=\n"),
			want: "Cannot use shared provider: missing OPENAI_BASE_URL in %[1]s/custom/provider.env",
		},
		{
			name: "base URL with an unsupported scheme",
			env:  ptr("SNAP_NAME=custom\nOPENAI_BASE_URL=ftp://example.com\n"),
			want: "Cannot use shared provider: invalid OPENAI_BASE_URL in %[1]s/custom/provider.env: scheme must be http or https",
		},
		{
			name: "base URL without a host",
			env:  ptr("SNAP_NAME=custom\nOPENAI_BASE_URL=http:///v1\n"),
			want: "Cannot use shared provider: invalid OPENAI_BASE_URL in %[1]s/custom/provider.env: has no host",
		},
		{
			name: "missing snap name",
			env:  ptr("OPENAI_BASE_URL=http://localhost:8081/v1\n"),
			want: "Cannot use shared provider: missing SNAP_NAME key in %[1]s/custom/provider.env",
		},
		{
			name: "invalid snap name",
			env:  ptr("SNAP_NAME=Not_A_Snap\nOPENAI_BASE_URL=http://localhost:8081/v1\n"),
			want: "Cannot use shared provider: invalid SNAP_NAME key in %[1]s/custom/provider.env",
		},
		{
			name: "snap name that is too long",
			env:  ptr("SNAP_NAME=" + strings.Repeat("a", 41) + "\nOPENAI_BASE_URL=http://localhost:8081/v1\n"),
			want: "Cannot use shared provider: invalid SNAP_NAME key in %[1]s/custom/provider.env",
		},
		{
			name: "unreadable env file",
			env:  ptr("SNAP_NAME='unterminated\n"),
			want: "Cannot use shared provider: cannot read %[1]s/custom/provider.env: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.env == nil {
				if err := os.Mkdir(filepath.Join(root, "custom"), 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				writeProviderEnv(t, root, "custom", *tt.env)
			}

			got, issues, err := ConnectedSnapProviders(root)
			if err != nil {
				t.Fatalf("ConnectedSnapProviders: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("got providers %+v, want the entry to be rejected", got)
			}
			want := fmt.Sprintf(tt.want, root)
			if len(issues) != 1 || !strings.HasPrefix(issues[0].Details, want) {
				t.Fatalf("got issues %+v, want one starting with %q", issues, want)
			}
		})
	}
}

func TestConnectedSnapProvidersDoesNotLeakCredentialsInIssues(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "custom", "SNAP_NAME=custom\nOPENAI_BASE_URL=http://user:secret@exa mple.com/v1\n")

	_, issues, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Details, "invalid OPENAI_BASE_URL") ||
		strings.Contains(issues[0].Details, "secret") {
		t.Fatalf("got issues %+v, want an invalid URL issue without the credentials", issues)
	}
}

func TestConnectedSnapProvidersIgnoresSnapInstanceName(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "a", "SNAP_NAME=gemma4\nSNAP_INSTANCE_NAME=gemma4_2\nOPENAI_BASE_URL=http://localhost:8080/v1\n")
	writeProviderEnv(t, root, "b", "SNAP_NAME=qwen3\nSNAP_INSTANCE_NAME=\nOPENAI_BASE_URL=http://localhost:8081/v1\n")

	got, issues, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	if len(got) != 2 || got[0].Name != "gemma4" || got[1].Name != "qwen3" || len(issues) != 0 {
		t.Fatalf("got providers %+v and issues %+v, want gemma4 and qwen3 without issues", got, issues)
	}
}

func TestConnectedSnapProvidersKeepsFirstProviderOnDuplicateSnapName(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "a", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=same\n")
	writeProviderEnv(t, root, "b", "OPENAI_BASE_URL=http://localhost:8081/v1\nSNAP_NAME=same\n")
	writeProviderEnv(t, root, "c", "OPENAI_BASE_URL=http://localhost:8082/v1\nSNAP_NAME=same\n")

	got, issues, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	if len(got) != 1 || got[0].Name != "same" || got[0].BaseURL != "http://localhost:8080/v1" {
		t.Fatalf("got %+v, want only the first provider", got)
	}
	first := filepath.Join(root, "a", "provider.env")
	wantIssues := []Issue{
		{Details: fmt.Sprintf("Cannot use shared provider: duplicate provider name %q in %s, already defined in %s",
			"same", filepath.Join(root, "b", "provider.env"), first)},
		{Details: fmt.Sprintf("Cannot use shared provider: duplicate provider name %q in %s, already defined in %s",
			"same", filepath.Join(root, "c", "provider.env"), first)},
	}
	if !reflect.DeepEqual(issues, wantIssues) {
		t.Fatalf("got issues %+v, want %+v", issues, wantIssues)
	}
}

func TestConnectedSnapProvidersRejectedEntryDoesNotTakeTheName(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "a", "SNAP_NAME=same\n")
	writeProviderEnv(t, root, "b", "OPENAI_BASE_URL=http://localhost:8081/v1\nSNAP_NAME=same\n")

	got, issues, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	if len(got) != 1 || got[0].BaseURL != "http://localhost:8081/v1" || len(issues) != 1 {
		t.Fatalf("got providers %+v and issues %+v, want the valid provider kept", got, issues)
	}
}

func TestConnectedSnapProvidersKeepsValidProvidersNextToInvalidOnes(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "broken", "SNAP_NAME=broken\nOPENAI_BASE_URL=not a url\n")
	writeProviderEnv(t, root, "healthy", "SNAP_NAME=healthy\nOPENAI_BASE_URL=http://localhost:8080/v1\n")
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, issues, err := ConnectedSnapProviders(root)
	if err != nil {
		t.Fatalf("ConnectedSnapProviders: %v", err)
	}
	if len(got) != 1 || got[0].Name != "healthy" {
		t.Fatalf("got %+v, want only the healthy provider", got)
	}
	if len(issues) != 2 {
		t.Fatalf("got issues %+v, want one for each rejected directory", issues)
	}
}

func TestRejectConfiguredCollisions(t *testing.T) {
	connected := []connectedProvider{
		{Provider: Provider{Name: "gemma4"}, envPath: "/share/a/provider.env"},
		{Provider: Provider{Name: "openai"}, envPath: "/share/b/provider.env"},
	}
	configured := []Provider{{Name: "openai", Backend: BackendProxy}}

	accepted, issues := rejectConfiguredCollisions(connected, configured)
	if len(accepted) != 1 || accepted[0].Name != "gemma4" {
		t.Fatalf("got accepted %+v, want only gemma4", accepted)
	}
	want := []Issue{{Details: `Cannot use shared provider: provider name "openai" in /share/b/provider.env is already used by a configured provider`}}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("got issues %+v, want %+v", issues, want)
	}
}

func TestServiceListsAndReportsIssuesSeparately(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "healthy", "SNAP_NAME=healthy\nOPENAI_BASE_URL=http://localhost:8080/v1\n")
	writeProviderEnv(t, root, "socket-only", "SNAP_NAME=socket-only\nUNIX_SOCKET=x.sock\n")
	if err := os.Mkdir(filepath.Join(root, "without-env"), 0o755); err != nil {
		t.Fatal(err)
	}
	client, _ := snapd.NewFakeServer(t, map[string]string{"healthy": snapd.SnapStatusActive})
	service := NewService(snapcatalog.WriteFakeCatalog(t, `[]`), client, root)

	list, err := service.ListInstalled(context.Background())
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(list) != 1 || list[0].Name != "healthy" || list[0].State != StateEnabled {
		t.Fatalf("got %+v, want only the healthy provider", list)
	}
	if _, err := service.Find(context.Background(), "socket-only"); err == nil {
		t.Fatal("Find returned a rejected provider, want an unknown provider error")
	}

	issues, err := service.Issues(context.Background())
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	want := []Issue{
		{Details: fmt.Sprintf("Cannot use shared provider: missing OPENAI_BASE_URL in %s", filepath.Join(root, "socket-only", "provider.env"))},
		{Details: fmt.Sprintf("Cannot use shared provider: missing provider.env in %s/", filepath.Join(root, "without-env"))},
	}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("got issues %+v, want %+v", issues, want)
	}
}

func ptr[T any](value T) *T {
	return &value
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

	result, err := NewService(catalog, client, root).List(context.Background())
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

	_, err := NewService(catalog, client, root).ListInstalled(context.Background())
	if err == nil {
		t.Fatal("got nil error, want snapd error")
	}
}

func TestListFailsWhenCatalogFails(t *testing.T) {
	root := t.TempDir()
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=custom\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{"custom": snapd.SnapStatusActive})

	_, err := NewService(snapcatalog.WriteFakeCatalog(t, "not json"), client, root).List(context.Background())
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
	_, err := NewService(catalog, client, root).List(context.Background())
	if err == nil {
		t.Fatal("got nil error, want connected source error")
	}
}
