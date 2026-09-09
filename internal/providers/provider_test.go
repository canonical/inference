package providers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func TestProviderInstalled(t *testing.T) {
	tests := []struct {
		name string
		p    Provider
		want bool
	}{
		{name: "unknown snap", p: Provider{Type: TypeInferenceSnap, State: StateUnknown}, want: false},
		{name: "not installed snap", p: Provider{Type: TypeInferenceSnap, State: StateNotInstalled}, want: false},
		{name: "disabled snap", p: Provider{Type: TypeInferenceSnap, State: StateDisabled}, want: true},
		{name: "enabled snap", p: Provider{Type: TypeInferenceSnap, State: StateEnabled}, want: true},
		{name: "configured OpenAI provider", p: Provider{Type: TypeOpenAI}, want: true},
		{name: "unknown provider type", p: Provider{Type: ProviderType("unknown")}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Installed(); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func writeCatalog(t *testing.T, entries string) *snapcatalog.Reader {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, snapcatalog.Filename)
	if err := os.WriteFile(path, []byte(entries), 0o644); err != nil {
		t.Fatalf("writing catalog: %v", err)
	}
	return &snapcatalog.Reader{Path: path}
}

func newSnapdServer(t *testing.T, statuses map[string]string) *snapd.Client {
	t.Helper()

	dir := t.TempDir()
	socket := filepath.Join(dir, "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on unix socket: %v", err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/v2/snaps/"):]
		status, ok := statuses[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"type":"error","status":"Not Found","status-code":404,"result":{
				"message":"snap \"%s\" not found",
				"kind":"snap-not-found"
			}}`, name)
			return
		}
		fmt.Fprintf(w, `{"type":"sync","status":"OK","result":{"name":%q,"status":%q}}`, name, status)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	return &snapd.Client{Socket: socket}
}

func TestList(t *testing.T) {
	catalog := writeCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","full_name":"canonical/gemma4","html_url":"https://example.com/gemma4"},
		{"snap":"qwen3","model_name":"Qwen 3","full_name":"canonical/qwen3","html_url":"https://example.com/qwen3"},
		{"snap":"smollm2","model_name":"SmolLM2","full_name":"canonical/smollm2","html_url":"https://example.com/smollm2"}
	]`)
	client := newSnapdServer(t, map[string]string{
		"gemma4": snapd.SnapStatusActive,
		"qwen3":  snapd.SnapStatusInstalled,
	})

	t.Run("all providers", func(t *testing.T) {
		got, err := List(context.Background(), catalog, client, "", ListOptions{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		want := []Provider{
			{Name: "gemma4", Type: TypeInferenceSnap, State: StateEnabled, Connection: ConnectionNotConnected},
			{Name: "qwen3", Type: TypeInferenceSnap, State: StateDisabled, Connection: ConnectionNotConnected},
			{Name: "smollm2", Type: TypeInferenceSnap, State: StateNotInstalled, Connection: ConnectionNotConnected},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d providers, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("provider %d: got %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("installed only", func(t *testing.T) {
		got, err := List(context.Background(), catalog, client, "", ListOptions{InstalledOnly: true})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		want := []Provider{
			{Name: "gemma4", Type: TypeInferenceSnap, State: StateEnabled, Connection: ConnectionNotConnected},
			{Name: "qwen3", Type: TypeInferenceSnap, State: StateDisabled, Connection: ConnectionNotConnected},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d providers, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("provider %d: got %+v, want %+v", i, got[i], want[i])
			}
		}
	})
}

func TestListTreatsMissingCatalogAsEmpty(t *testing.T) {
	client := newSnapdServer(t, nil)

	for _, catalog := range []*snapcatalog.Reader{
		{},
		{Path: filepath.Join(t.TempDir(), "missing.json")},
	} {
		got, err := List(context.Background(), catalog, client, "", ListOptions{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no providers", got)
		}
	}
}

func TestListReturnsMalformedCatalogError(t *testing.T) {
	catalog := writeCatalog(t, "not json")
	client := newSnapdServer(t, nil)

	if _, err := List(context.Background(), catalog, client, "", ListOptions{}); err == nil {
		t.Fatal("expected malformed catalog error")
	}
}

func TestSnapStatusToProviderState(t *testing.T) {
	tests := []struct {
		name       string
		snapStatus string
		want       LifecycleState
		wantErr    bool
	}{
		{
			name:       "not installed",
			snapStatus: snapd.StatusNotInstalled,
			want:       StateNotInstalled,
		},
		{
			name:       "active",
			snapStatus: snapd.SnapStatusActive,
			want:       StateEnabled,
		},
		{
			name:       "installed",
			snapStatus: snapd.SnapStatusInstalled,
			want:       StateDisabled,
		},
		{
			name:       "unexpected status",
			snapStatus: "bogus",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := snapStatusToProviderState(tt.snapStatus)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
