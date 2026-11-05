package status

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func testOptions(t *testing.T, snapName string, active bool) Options {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/apps":
			if got := r.URL.Query().Get("names"); got != snapName+".d" {
				t.Errorf("got service %q, want %q", got, snapName+".d")
			}
			if _, err := fmt.Fprintf(w, `{"type":"sync","result":[{"snap":%q,"name":"d","active":%t}]}`, snapName, active); err != nil {
				t.Errorf("writing apps response: %v", err)
			}
		case strings.HasPrefix(r.URL.Path, "/v2/snaps/"):
			name := strings.TrimPrefix(r.URL.Path, "/v2/snaps/")
			state := snapd.SnapStatusActive
			if name == "disabled" {
				state = snapd.SnapStatusInstalled
			}
			if _, err := fmt.Fprintf(w, `{"type":"sync","result":{"name":%q,"status":%q}}`, name, state); err != nil {
				t.Errorf("writing snap response: %v", err)
			}
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	if err := server.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return Options{
		SnapCatalog: &snapcatalog.Reader{},
		SnapdClient: &snapd.Client{Socket: socket},
		HTTPHost:    "127.0.0.1",
		HTTPPort:    "8400",
	}
}

func TestLookup(t *testing.T) {
	for _, test := range []struct {
		name     string
		instance string
		active   bool
		host     string
		port     string
		wantURL  string
		want     string
	}{
		{"fallback", "", true, "127.0.0.1", "8400", "http://127.0.0.1:8400/v1", snapd.ServiceStatusActive},
		{"instance", "inference_gpu", true, "127.0.0.1", "8400", "http://127.0.0.1:8400/v1", snapd.ServiceStatusActive},
		{"inactive", "", false, "127.0.0.1", "8400", "http://127.0.0.1:8400/v1", snapd.ServiceStatusInactive},
		{"IPv6 and custom port", "", true, "::1", "8401", "http://[::1]:8401/v1", snapd.ServiceStatusActive},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SNAP_INSTANCE_NAME", test.instance)
			snapName := test.instance
			if snapName == "" {
				snapName = "inference"
			}
			options := testOptions(t, snapName, test.active)
			options.HTTPHost = test.host
			options.HTTPPort = test.port
			got, err := Lookup(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			want := Status{
				Services: map[string]string{snapName + ".d": test.want},
				Proxy: map[string]map[string]string{
					"openai": {"base-url": test.wantURL},
				},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestLookupProviderHealth(t *testing.T) {
	t.Setenv("SNAP_INSTANCE_NAME", "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthy/v1/models":
			if _, err := fmt.Fprint(w, `{"data":[{"id":"model"}]}`); err != nil {
				t.Errorf("writing models response: %v", err)
			}
		case "/unhealthy/v1/models":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected health request %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	options := testOptions(t, "inference", true)
	options.ShareProvidersPath = t.TempDir()
	options.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[{"snap":"disconnected"}]`)
	for _, name := range []string{"healthy", "unhealthy", "disabled"} {
		dir := filepath.Join(options.ShareProvidersPath, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		env := fmt.Sprintf("SNAP_NAME=%s\nOPENAI_BASE_URL=%s/%s/v1\n", name, upstream.URL, name)
		if err := os.WriteFile(filepath.Join(dir, "provider.env"), []byte(env), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Lookup(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"healthy":   "ok",
		"unhealthy": "error",
	}
	if !reflect.DeepEqual(got.Health, want) {
		t.Fatalf("got health %v, want %v", got.Health, want)
	}
}

func TestLookupErrors(t *testing.T) {
	t.Setenv("SNAP_INSTANCE_NAME", "")
	t.Run("snapd", func(t *testing.T) {
		options := testOptions(t, "inference", true)
		options.SnapdClient.Socket = filepath.Join(t.TempDir(), "missing.socket")
		if _, err := Lookup(t.Context(), options); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("got error %v, want missing socket error", err)
		}
	})
	t.Run("providers", func(t *testing.T) {
		options := testOptions(t, "inference", true)
		options.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `invalid`)
		if _, err := Lookup(t.Context(), options); err == nil {
			t.Fatal("expected invalid catalog error")
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		options := testOptions(t, "inference", true)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := Lookup(ctx, options); !errors.Is(err, context.Canceled) {
			t.Fatalf("got error %v, want cancellation", err)
		}
	})
}
