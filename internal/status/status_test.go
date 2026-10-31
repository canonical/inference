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

	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func testCollector(t *testing.T, instance string, active bool, states map[string]string) Collector {
	t.Helper()
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_INSTANCE_NAME", instance)
	socket := filepath.Join(t.TempDir(), "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/apps" {
			name := instance
			if name == "" {
				name = "inference"
			}
			if got := r.URL.Query().Get("names"); got != name+".d" {
				t.Errorf("service query = %q, want %q", got, name+".d")
			}
			fmt.Fprintf(w, `{"type":"sync","status":"OK","result":[{"snap":%q,"name":"d","active":%t}]}`, name, active)
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/v2/snaps/"); ok {
			if state, exists := states[name]; exists {
				fmt.Fprintf(w, `{"type":"sync","status":"OK","result":{"name":%q,"status":%q}}`, name, state)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"type":"error","status":"Not Found","status-code":404,"result":{"kind":"snap-not-found","message":"snap not found"}}`)
			return
		}
		t.Errorf("unexpected snapd path %q", r.URL.Path)
		http.NotFound(w, r)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return Collector{
		SnapdClient:        &snapd.Client{Socket: socket},
		SnapCatalog:        &snapcatalog.Reader{},
		ShareProvidersPath: t.TempDir(),
		BaseURL:            "http://[::1]:8401/v1",
	}
}

func TestCollectServices(t *testing.T) {
	for _, test := range []struct {
		name     string
		instance string
		active   bool
		service  string
		state    string
	}{
		{name: "default", active: true, service: "inference.d", state: "active"},
		{name: "parallel instance", instance: "inference_gpu", service: "inference_gpu.d", state: "inactive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			collector := testCollector(t, test.instance, test.active, nil)
			got, err := collector.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := Status{
				Services: map[string]string{test.service: test.state},
				BaseURL:  collector.BaseURL,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("status = %+v, want %+v", got, want)
			}
		})
	}
}

func TestCollectInstalledProviderHealth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthy/v1/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"ready"}]}`)
		case "/unhealthy/v1/models":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			t.Errorf("queried unavailable provider %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	collector := testCollector(t, "", true, map[string]string{
		"healthy":   snapd.SnapStatusActive,
		"unhealthy": snapd.SnapStatusActive,
		"disabled":  snapd.SnapStatusInstalled,
	})
	collector.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"healthy"},{"snap":"unhealthy"},{"snap":"disabled"},{"snap":"absent"}
	]`)
	for _, name := range []string{"healthy", "unhealthy", "disabled", "absent"} {
		dir := filepath.Join(collector.ShareProvidersPath, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		env := "SNAP_NAME=" + name + "\nOPENAI_BASE_URL=" + upstream.URL + "/" + name + "/v1\n"
		if err := os.WriteFile(filepath.Join(dir, "provider.env"), []byte(env), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	list, err := collector.ListProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, provider := range list {
		names = append(names, provider.Name)
		if !provider.Installed() {
			t.Errorf("listed uninstalled provider %+v", provider)
		}
		if provider.Name == "disabled" && provider.State != providers.StateDisabled {
			t.Errorf("disabled provider state = %q", provider.State)
		}
	}
	if want := []string{"disabled", "healthy", "unhealthy"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("providers = %v, want %v", names, want)
	}

	got, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"healthy": "ok", "unhealthy": "error"}
	if !reflect.DeepEqual(got.Health, want) {
		t.Fatalf("health = %v, want %v", got.Health, want)
	}
}

func TestCollectErrors(t *testing.T) {
	t.Run("snapd unavailable", func(t *testing.T) {
		collector := testCollector(t, "", true, nil)
		collector.SnapdClient = &snapd.Client{Socket: filepath.Join(t.TempDir(), "missing.socket")}
		_, err := collector.Collect(context.Background())
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("error = %v, want missing socket", err)
		}
	})
	t.Run("provider inventory unavailable", func(t *testing.T) {
		collector := testCollector(t, "", true, nil)
		collector.SnapCatalog = snapcatalog.WriteFakeCatalog(t, "invalid json")
		if _, err := collector.Collect(context.Background()); err == nil {
			t.Fatal("expected inventory error")
		}
	})
	t.Run("cancelled request", func(t *testing.T) {
		collector := testCollector(t, "", true, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := collector.Collect(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", err)
		}
	})
}
