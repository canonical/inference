package commands

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func TestInfoRendering(t *testing.T) {
	tests := []struct {
		name     string
		provider providers.Provider
		want     string
	}{
		{
			name: "fully configured provider",
			provider: providers.Provider{
				Name:    "gemma4",
				Type:    providers.TypeInferenceSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://localhost:8336/v1",
			},
			want: `name: gemma4
type: inference-snap
state: enabled
api
  openai
    base-url: http://localhost:8336/v1
`,
		},
		{
			name: "redact BaseURL with credentials and query",
			provider: providers.Provider{
				Name:    "gemma4",
				Type:    providers.TypeInferenceSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://user:token@localhost:8336/v1?api_key=secret",
			},
			want: `name: gemma4
type: inference-snap
state: enabled
api
  openai
    base-url: http://localhost:8336/v1
`,
		},
		{
			name: "provider without a BaseURL",
			provider: providers.Provider{
				Name:  "gemma4",
				Type:  providers.TypeInferenceSnap,
				State: providers.StateEnabled,
			},
			want: `name: gemma4
type: inference-snap
state: enabled
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderInfo(tt.provider)
			if got != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestInfo_PositionalArgsAreRequired(t *testing.T) {
	ctx, stdout, _ := newTestContext()

	if err := execute(Info(ctx)); err == nil {
		t.Fatal("expected error when no provider name is given")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for a missing argument, got %q", stdout.String())
	}
}

func TestInfo_TooManyPositionalArgsAreRejected(t *testing.T) {
	ctx, stdout, _ := newTestContext()

	if err := execute(Info(ctx), "gemma4", "qwen3"); err == nil {
		t.Fatal("expected error for more than one positional argument")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for unexpected positional arguments, got %q", stdout.String())
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

func TestInfo_PrintsProvider(t *testing.T) {
	ctx, stdout, _ := newTestContext()
	ctx.SnapCatalog = writeCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","full_name":"canonical/gemma4","html_url":"https://example.com/gemma4"}
	]`)
	ctx.SnapdClient = newSnapdServer(t, map[string]string{"gemma4": snapd.SnapStatusActive})

	if err := execute(Info(ctx), "gemma4"); err != nil {
		t.Fatalf("Info: %v", err)
	}

	want := "name: gemma4\ntype: inference-snap\nstate: enabled\n"
	if stdout.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", stdout.String(), want)
	}
}

func TestInfo_UnknownProvider(t *testing.T) {
	ctx, stdout, _ := newTestContext()
	ctx.SnapCatalog = writeCatalog(t, `[]`)
	ctx.SnapdClient = newSnapdServer(t, nil)

	err := execute(Info(ctx), "some-imaginary-provider")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "some-imaginary-provider") {
		t.Fatalf("expected error to mention the requested name, got: %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output on error, got %q", stdout.String())
	}
}
