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
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/canonical/inference/internal/storage"
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
	_ = server.Listener.Close()
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
	ctx.Storage = &storage.Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	return ctx
}

func writeAccepted(w http.ResponseWriter, changeID string) {
	w.WriteHeader(http.StatusAccepted)
	_, _ = fmt.Fprintf(w, `{"type":"async","status":"Accepted","status-code":202,"change":%q}`, changeID)
}

func writeChangeDone(w http.ResponseWriter) {
	_, _ = fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Done","ready":true}}`)
}

func writeSnapdError(w http.ResponseWriter, kind string) {
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(w, `{"type":"error","status":"Bad Request","status-code":400,"result":{"message":"failed","kind":%q}}`, kind)
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

func TestInstallAddsProviderToStorage(t *testing.T) {
	ctx := newInstallRemoveContext(t, installingSnapd(t))

	if err := execute(Install(ctx), "gemma4"); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := ctx.Storage.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]storage.Provider{"gemma4": {Enabled: true}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInstallKeepsExistingProviderSetting(t *testing.T) {
	ctx := newInstallRemoveContext(t, installingSnapd(t))
	if _, err := ctx.Storage.SetProviderEnabled("gemma4", false); err != nil {
		t.Fatal(err)
	}

	if err := execute(Install(ctx), "gemma4"); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := ctx.Storage.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if got["gemma4"].Enabled {
		t.Fatal("install should not re-enable a disabled provider")
	}
}

func TestInstallFailureDoesNotTouchStorage(t *testing.T) {
	ctx := newInstallRemoveContext(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSnapdError(w, "snap-not-found")
	})

	if err := execute(Install(ctx), "gemma4"); err == nil {
		t.Fatal("expected install error")
	}

	if _, err := os.Stat(ctx.Storage.Path); !os.IsNotExist(err) {
		t.Fatalf("storage file should not be created, stat error = %v", err)
	}
}

func TestRemoveDeletesProviderFromStorage(t *testing.T) {
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
	if _, err := ctx.Storage.EnsureProviders([]string{"gemma4", "other"}); err != nil {
		t.Fatal(err)
	}

	if err := execute(Remove(ctx), "gemma4"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got, err := ctx.Storage.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]storage.Provider{"other": {Enabled: true}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRemoveWithoutStorageEntrySucceeds(t *testing.T) {
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

func TestRemoveNotInstalledSnapDeletesStaleStorageEntry(t *testing.T) {
	ctx := newInstallRemoveContext(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSnapdError(w, "snap-not-installed")
	})
	if _, err := ctx.Storage.EnsureProviders([]string{"gemma4"}); err != nil {
		t.Fatal(err)
	}

	if err := execute(Remove(ctx), "gemma4"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if exists, err := ctx.Storage.ProviderExists("gemma4"); err != nil || exists {
		t.Fatalf("ProviderExists = %t, %v; want false, nil", exists, err)
	}
}

func TestRemoveFailureKeepsStorageEntry(t *testing.T) {
	ctx := newInstallRemoveContext(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSnapdError(w, "snap-change-conflict")
	})
	if _, err := ctx.Storage.EnsureProviders([]string{"gemma4"}); err != nil {
		t.Fatal(err)
	}

	if err := execute(Remove(ctx), "gemma4"); err == nil {
		t.Fatal("expected remove error")
	}

	if exists, err := ctx.Storage.ProviderExists("gemma4"); err != nil || !exists {
		t.Fatalf("ProviderExists = %t, %v; want true, nil", exists, err)
	}
}
