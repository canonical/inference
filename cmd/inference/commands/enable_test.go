package commands

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/spf13/cobra"
)

type enableFixture struct {
	ctx        *common.Context
	stdout     func() string
	stderr     func() string
	config     *config.ConfigReader
	discovered *atomic.Int32
}

func newEnableFixture(t *testing.T) enableFixture {
	t.Helper()
	ctx, stdout, stderr := newTestContext()
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","repo_url":"https://example.com/gemma4"},
		{"snap":"qwen3","model_name":"Qwen 3","repo_url":"https://example.com/qwen3"}
	]`)
	ctx.SnapdClient = newEnableSnapdClient(t, map[string]string{"gemma4": snapd.SnapStatusActive}, true)
	ctx.Config = &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}

	var discovered atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			discovered.Add(1)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(daemon.Close)
	setDaemonAddress(t, ctx, daemon.URL)

	return enableFixture{
		ctx:        ctx,
		stdout:     stdout.String,
		stderr:     stderr.String,
		config:     ctx.Config,
		discovered: &discovered,
	}
}

func newEnableSnapdClient(t *testing.T, snapStatuses map[string]string, serviceActive bool) *snapd.Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/apps" {
			fmt.Fprintf(w, `{"type":"sync","status":"OK","result":[{"snap":"inference","name":"d","active":%t}]}`, serviceActive)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/v2/snaps/")
		status, ok := snapStatuses[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"type":"error","status":"Not Found","status-code":404,"result":{"message":"snap not found","kind":"snap-not-found"}}`)
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

func setDaemonAddress(t *testing.T, ctx *common.Context, rawURL string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx.HTTPHost, ctx.HTTPPort, err = net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDisableThenEnable(t *testing.T) {
	f := newEnableFixture(t)

	if err := execute(Disable(f.ctx), "gemma4"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	providers, err := f.config.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if providers["gemma4"].Enabled {
		t.Fatal("gemma4 should be disabled in the config")
	}
	if got, want := f.stdout(), "Disabled gemma4\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if f.discovered.Load() != 1 {
		t.Fatalf("model discovery triggered %d times, want 1", f.discovered.Load())
	}

	if err := execute(Enable(f.ctx), "gemma4"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	providers, err = f.config.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if !providers["gemma4"].Enabled {
		t.Fatal("gemma4 should be enabled in the config")
	}
	if f.discovered.Load() != 2 {
		t.Fatalf("model discovery triggered %d times, want 2", f.discovered.Load())
	}
}

func TestEnableAlreadyEnabledSucceeds(t *testing.T) {
	f := newEnableFixture(t)

	if err := execute(Enable(f.ctx), "gemma4"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got, want := f.stdout(), "gemma4 is already enabled\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestDisableAlreadyDisabledSucceeds(t *testing.T) {
	f := newEnableFixture(t)
	if _, err := f.config.SetProviderEnabled("gemma4", false); err != nil {
		t.Fatal(err)
	}

	if err := execute(Disable(f.ctx), "gemma4"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got, want := f.stdout(), "gemma4 is already disabled\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEnableDisableRejectUnknownAndNotInstalledProviders(t *testing.T) {
	commands := map[string]func(*common.Context) *cobra.Command{"enable": Enable, "disable": Disable}
	for commandName, command := range commands {
		for name, wantErr := range map[string]string{
			"unknown": "unknown provider",
			"qwen3":   "not installed",
		} {
			f := newEnableFixture(t)
			err := execute(command(f.ctx), name)
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("%s %s: got error %v, want containing %q", commandName, name, err, wantErr)
			}
			if f.discovered.Load() != 0 {
				t.Fatalf("%s %s: model discovery should not be triggered on failure", commandName, name)
			}
		}
	}
}

func TestDisableSkipsDiscoveryWhenServiceIsInactive(t *testing.T) {
	f := newEnableFixture(t)
	f.ctx.SnapdClient = newEnableSnapdClient(t, map[string]string{"gemma4": snapd.SnapStatusActive}, false)

	if err := execute(Disable(f.ctx), "gemma4"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if f.discovered.Load() != 0 {
		t.Fatalf("model discovery triggered %d times, want 0", f.discovered.Load())
	}
	providers, err := f.config.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if providers["gemma4"].Enabled {
		t.Fatal("config should still be updated")
	}
}

func TestDisableWarnsWhenDiscoveryFails(t *testing.T) {
	f := newEnableFixture(t)
	f.ctx.HTTPPort = "1"

	if err := execute(Disable(f.ctx), "gemma4"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got, want := f.stdout(), "Disabled gemma4\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(f.stderr(), "warning: triggering model discovery failed") {
		t.Fatalf("stderr = %q, want model discovery warning", f.stderr())
	}
	providers, err := f.config.GetProviders()
	if err != nil {
		t.Fatal(err)
	}
	if providers["gemma4"].Enabled {
		t.Fatal("config should still be updated")
	}
}

func TestEnableRequiresOneArgument(t *testing.T) {
	f := newEnableFixture(t)
	if err := execute(Enable(f.ctx)); err == nil {
		t.Fatal("expected error without arguments")
	}
	if err := execute(Disable(f.ctx), "a", "b"); err == nil {
		t.Fatal("expected error with two arguments")
	}
}

func TestDisableRequiresSudoWhenConfigIsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to read-only directories")
	}
	f := newEnableFixture(t)
	dir := filepath.Dir(f.config.Path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := execute(Disable(f.ctx), "gemma4")
	if err == nil || !strings.Contains(err.Error(), "Try again using sudo") {
		t.Fatalf("got error %v, want sudo hint", err)
	}
	if f.discovered.Load() != 0 {
		t.Fatal("model discovery should not be triggered when saving fails")
	}
}
