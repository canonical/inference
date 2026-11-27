package commands

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func newSnapdHandlerClient(t *testing.T, handler http.HandlerFunc) *snapd.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "inference-cmd-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return &snapd.Client{Socket: socket}
}

func newInstallRemoveContext(t *testing.T, handler http.HandlerFunc) *common.Context {
	t.Helper()
	ctx, _, _ := newTestContext()
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","repo_url":"https://example.com/gemma4"}
	]`)
	ctx.SnapdClient = newSnapdHandlerClient(t, handler)
	ctx.Config = &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	return ctx
}

func writeAccepted(w http.ResponseWriter, changeID string) {
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"type":"async","status":"Accepted","status-code":202,"change":%q}`, changeID)
}

func writeChangeDone(w http.ResponseWriter) {
	fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Done","ready":true}}`)
}

func writeSnapdError(w http.ResponseWriter, kind string) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `{"type":"error","status":"Bad Request","status-code":400,"result":{"message":"failed","kind":%q}}`, kind)
}

func installingSnapd(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/gemma4":
			writeAccepted(w, "install")
		case r.Method == http.MethodPost && r.URL.Path == "/v2/interfaces":
			writeAccepted(w, "connect")
		case r.Method == http.MethodGet && (r.URL.Path == "/v2/changes/install" || r.URL.Path == "/v2/changes/connect"):
			writeChangeDone(w)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}
}

func TestInstallAddsProviderToConfig(t *testing.T) {
	ctx := newInstallRemoveContext(t, installingSnapd(t))

	if err := execute(Install(ctx), "gemma4"); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := ctx.Config.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]config.Provider{"gemma4": {Enabled: true}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInstallKeepsExistingProviderSetting(t *testing.T) {
	ctx := newInstallRemoveContext(t, installingSnapd(t))
	if _, err := ctx.Config.SetProviderEnabled("gemma4", false); err != nil {
		t.Fatal(err)
	}

	if err := execute(Install(ctx), "gemma4"); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := ctx.Config.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if got["gemma4"].Enabled {
		t.Fatal("install should not re-enable a disabled provider")
	}
}

func TestInstallFailureDoesNotTouchConfig(t *testing.T) {
	ctx := newInstallRemoveContext(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSnapdError(w, "snap-not-found")
	})

	if err := execute(Install(ctx), "gemma4"); err == nil {
		t.Fatal("expected install error")
	}

	if _, err := os.Stat(ctx.Config.Path); !os.IsNotExist(err) {
		t.Fatalf("config file should not be created, stat error = %v", err)
	}
}

func TestRemoveDeletesProviderFromConfig(t *testing.T) {
	ctx := newInstallRemoveContext(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/gemma4":
			writeAccepted(w, "remove")
		case r.Method == http.MethodGet && r.URL.Path == "/v2/changes/remove":
			writeChangeDone(w)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	if _, err := ctx.Config.EnsureProviders([]string{"gemma4", "other"}); err != nil {
		t.Fatal(err)
	}

	if err := execute(Remove(ctx), "gemma4"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got, err := ctx.Config.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]config.Provider{"other": {Enabled: true}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRemoveWithoutConfigEntrySucceeds(t *testing.T) {
	ctx := newInstallRemoveContext(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/gemma4":
			writeAccepted(w, "remove")
		case r.Method == http.MethodGet && r.URL.Path == "/v2/changes/remove":
			writeChangeDone(w)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	if err := execute(Remove(ctx), "gemma4"); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestRemoveNotInstalledSnapDeletesStaleConfigEntry(t *testing.T) {
	ctx := newInstallRemoveContext(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSnapdError(w, "snap-not-installed")
	})
	if _, err := ctx.Config.EnsureProviders([]string{"gemma4"}); err != nil {
		t.Fatal(err)
	}

	if err := execute(Remove(ctx), "gemma4"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if exists, err := ctx.Config.ProviderExists("gemma4"); err != nil || exists {
		t.Fatalf("ProviderExists = %t, %v; want false, nil", exists, err)
	}
}

func TestRemoveFailureKeepsConfigEntry(t *testing.T) {
	ctx := newInstallRemoveContext(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSnapdError(w, "snap-change-conflict")
	})
	if _, err := ctx.Config.EnsureProviders([]string{"gemma4"}); err != nil {
		t.Fatal(err)
	}

	if err := execute(Remove(ctx), "gemma4"); err == nil {
		t.Fatal("expected remove error")
	}

	if exists, err := ctx.Config.ProviderExists("gemma4"); err != nil || !exists {
		t.Fatalf("ProviderExists = %t, %v; want true, nil", exists, err)
	}
}
