package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"go.yaml.in/yaml/v4"
)

func statusSnapdClient(t *testing.T, snapName string, active bool) *snapd.Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/apps" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("names"); got != snapName+".d" {
			t.Errorf("unexpected names query %q", got)
		}
		fmt.Fprintf(w, `{"type":"sync","status":"OK","result":[{"snap":%q,"name":"d","active":%t}]}`, snapName, active)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return &snapd.Client{Socket: socket}
}

func TestStatusServicesOutput(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "yaml",
			want: "services:\n  inference.d: active\nproxy:\n  openai:\n    base-url: http://127.0.0.1:8400/v1\n",
		},
		{
			name: "json",
			args: []string{"--format=json"},
			want: "{\n  \"services\": {\n    \"inference.d\": \"active\"\n  },\n  \"proxy\": {\n    \"openai\": {\n      \"base-url\": \"http://127.0.0.1:8400/v1\"\n    }\n  }\n}\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SNAP", "")
			t.Setenv("SNAP_INSTANCE_NAME", "")
			ctx, stdout, stderr := newTestContext(t)
			ctx.SnapdClient = statusSnapdClient(t, common.InferenceSnapName, true)
			ctx.SnapCatalog = &snapcatalog.Reader{}
			if err := execute(Status(ctx), test.args...); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != test.want {
				t.Fatalf("got:\n%s\nwant:\n%s", stdout.String(), test.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestStatusUsesSnapInstanceName(t *testing.T) {
	const instanceName = "inference_gpu"
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_INSTANCE_NAME", instanceName)
	ctx, stdout, _ := newTestContext(t)
	ctx.SnapdClient = statusSnapdClient(t, instanceName, true)
	ctx.SnapCatalog = &snapcatalog.Reader{}

	if err := execute(Status(ctx)); err != nil {
		t.Fatal(err)
	}
	want := "services:\n  inference_gpu.d: active\nproxy:\n  openai:\n    base-url: http://127.0.0.1:8400/v1\n"
	if stdout.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestProxyOpenAIBaseURL(t *testing.T) {
	got := (&statusCommand{Context: &common.Context{
		HTTPHost: "::1",
		HTTPPort: "8401",
	}}).proxyOpenAIBaseURL()
	if got != "http://[::1]:8401/v1" {
		t.Fatalf("got %q, want %q", got, "http://[::1]:8401/v1")
	}
}

func TestStatusRejectsArguments(t *testing.T) {
	if err := execute(Status(&common.Context{}), "unexpected"); err == nil {
		t.Fatal("expected arguments to be rejected")
	}
}

func TestStatusRejectsInvalidFormat(t *testing.T) {
	if err := execute(Status(&common.Context{}), "--format=xml"); err == nil {
		t.Fatal("expected invalid format to be rejected")
	}
}

func TestStatusOutputJSON(t *testing.T) {
	output := statusOutput{
		Services: map[string]string{"proxy": "active"},
		Proxy: &statusProxy{
			OpenAI: statusOpenAIProxy{BaseURL: "http://127.0.0.1:8080/v1"},
		},
		Health: map[string]string{
			"gemma4":      "ok",
			"qwen3.6":     "not responsive",
			"ollama":      "ok",
			"openai-work": "offline",
		},
		Warnings: []string{"Unable to communicate with NVIDIA GeForce RTX 4050 GPU."},
	}

	got, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"services":{"proxy":"active"},"proxy":{"openai":{"base-url":"http://127.0.0.1:8080/v1"}},"health":{"gemma4":"ok","ollama":"ok","openai-work":"offline","qwen3.6":"not responsive"},"warnings":["Unable to communicate with NVIDIA GeForce RTX 4050 GPU."]}`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func writeStatusProviders(t *testing.T, ctx *common.Context, providers map[string]config.Provider) {
	t.Helper()
	data, err := json.Marshal(config.Config{Version: 1, Providers: providers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctx.Config.Path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatusHealthUsesDaemonWithoutCredentials(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Setenv("SNAP_INSTANCE_NAME", "")
			var upstreamRequests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamRequests.Add(1)
				http.Error(w, "authentication required", http.StatusUnauthorized)
			}))
			t.Cleanup(upstream.Close)

			var daemonRequests atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				daemonRequests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "" {
					t.Errorf("unexpected daemon authorization: %q", got)
				}
				fmt.Fprint(w, `{"data":[
					{"id":"authenticated/first","owned_by":"authenticated"},
					{"id":"authenticated/second","owned_by":"authenticated"},
					{"id":"unknown/model","owned_by":"unknown"}
				]}`)
			}))
			t.Cleanup(daemon.Close)

			ctx, stdout, stderr := newTestContext(t)
			var err error
			ctx.HTTPHost, ctx.HTTPPort, err = net.SplitHostPort(daemon.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("INFERENCE_CONFIG_FILE", ctx.Config.Path)
			writeStatusProviders(t, ctx, map[string]config.Provider{
				"authenticated": {
					Enabled: true, Backend: config.BackendProxy,
					BaseURL: upstream.URL + "/v1", AuthScheme: config.AuthorizationAPIKey,
				},
			})
			if _, err := ctx.Config.ProviderAPIKey("authenticated"); err == nil {
				t.Fatal("expected credentials to be unavailable to the CLI")
			}
			ctx.SnapCatalog = &snapcatalog.Reader{}
			ctx.SnapdClient = statusSnapdClient(t, common.InferenceSnapName, true)
			if err := execute(Status(ctx), "--format="+format); err != nil {
				t.Fatal(err)
			}
			var got statusOutput
			if format == "json" {
				err = json.Unmarshal(stdout.Bytes(), &got)
			} else {
				err = yaml.Unmarshal(stdout.Bytes(), &got)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got.Health, map[string]string{"authenticated": "ok"}) {
				t.Fatalf("health = %v, want only authenticated: ok", got.Health)
			}
			if got.Services["inference.d"] != "active" || got.Proxy == nil ||
				got.Proxy.OpenAI.BaseURL != "http://"+net.JoinHostPort(ctx.HTTPHost, ctx.HTTPPort)+"/v1" {
				t.Fatalf("unexpected status: %+v", got)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", stderr.String())
			}
			if got := daemonRequests.Load(); got != 1 {
				t.Fatalf("daemon requests = %d, want 1", got)
			}
			if got := upstreamRequests.Load(); got != 0 {
				t.Fatalf("upstream requests = %d, want 0", got)
			}
		})
	}
}

func TestStatusHealthPreservesProviderEligibility(t *testing.T) {
	ctx := modelsDaemon(t, http.StatusOK, `{"data":[
		{"id":"configured/model","owned_by":"configured"},
		{"id":"connected/model","owned_by":"connected"},
		{"id":"disabled/model","owned_by":"disabled"},
		{"id":"no-url/model","owned_by":"no-url"},
		{"id":"uninstalled/model","owned_by":"uninstalled"}
	]}`)
	writeStatusProviders(t, ctx, map[string]config.Provider{
		"configured": {Enabled: true, Backend: config.BackendProxy, BaseURL: "http://unused.invalid/v1"},
		"disabled":   {Enabled: false, Backend: config.BackendProxy, BaseURL: "http://unused.invalid/v1"},
		"no-url":     {Enabled: true, Backend: config.BackendProxy},
	})
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"connected"},{"snap":"disabled-snap"},{"snap":"uninstalled"},{"snap":"unconnected"}
	]`)
	ctx.SnapdClient, _ = snapd.NewFakeServer(t, map[string]string{
		"connected":     snapd.SnapStatusActive,
		"disabled-snap": snapd.SnapStatusInstalled,
		"unconnected":   snapd.SnapStatusActive,
	})
	ctx.ShareProvidersPath = t.TempDir()
	for _, name := range []string{"connected", "disabled-snap", "uninstalled"} {
		directory := filepath.Join(ctx.ShareProvidersPath, name)
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "provider.env"),
			[]byte("SNAP_NAME="+name+"\nOPENAI_BASE_URL=http://unused.invalid/v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := (&statusCommand{Context: ctx}).providerHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"configured": "ok", "connected": "ok"}
	if !maps.Equal(got, want) {
		t.Fatalf("health = %v, want %v", got, want)
	}
}

func TestStatusHealthDiagnosesMissingModelsWithoutCredentials(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, want: "error"},
		{name: "forbidden", status: http.StatusForbidden, want: "error"},
		{name: "upstream error", status: http.StatusInternalServerError, want: "error"},
		{name: "invalid json", status: http.StatusOK, body: `{`, want: "error"},
		{name: "empty models", status: http.StatusOK, body: `{"data":[]}`, want: "error"},
		{name: "unauthenticated models do not establish health", status: http.StatusOK, body: `{"data":[{"id":"model"}]}`, want: "error"},
		{name: "offline", want: "offline"},
		{name: "timeout", want: "not responsive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var upstreamRequests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamRequests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "" {
					t.Errorf("unexpected upstream authorization: %q", got)
				}
				if test.name == "timeout" {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			t.Cleanup(upstream.Close)
			if test.name == "offline" {
				upstream.Close()
			}
			ctx := modelsDaemon(t, http.StatusOK, `{"data":[]}`)
			t.Setenv("INFERENCE_CONFIG_FILE", ctx.Config.Path)
			writeStatusProviders(t, ctx, map[string]config.Provider{
				"provider": {
					Enabled: true, Backend: config.BackendProxy,
					BaseURL: upstream.URL + "/v1", AuthScheme: config.AuthorizationAPIKey,
				},
			})
			ctx.SnapCatalog = &snapcatalog.Reader{}
			checkCtx := context.Background()
			if test.name == "timeout" {
				var cancel context.CancelFunc
				checkCtx, cancel = context.WithTimeout(checkCtx, 250*time.Millisecond)
				defer cancel()
			}
			got, err := (&statusCommand{Context: ctx}).providerHealth(checkCtx)
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, map[string]string{"provider": test.want}) {
				t.Fatalf("health = %v, want provider: %s", got, test.want)
			}
			wantRequests := int32(1)
			if test.name == "offline" {
				wantRequests = 0
			}
			if got := upstreamRequests.Load(); got != wantRequests {
				t.Fatalf("upstream requests = %d, want %d", got, wantRequests)
			}
		})
	}
}

func TestStatusReportsDaemonErrors(t *testing.T) {
	t.Setenv("SNAP_INSTANCE_NAME", "")
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "unavailable", status: http.StatusServiceUnavailable, want: "returned 503"},
		{name: "invalid json", status: http.StatusOK, body: `{`, want: "decoding daemon models"},
		{name: "missing data", status: http.StatusOK, body: `{}`, want: "missing data array"},
		{name: "invalid model", status: http.StatusOK, body: `{"data":[{"id":"unqualified","owned_by":"provider"}]}`, want: "invalid model ID"},
		{name: "unreachable", status: http.StatusOK, body: `{"data":[]}`, want: "requesting models from daemon"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := modelsDaemon(t, test.status, test.body)
			var upstreamRequests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamRequests.Add(1)
				fmt.Fprint(w, `{"data":[{"id":"model"}]}`)
			}))
			t.Cleanup(upstream.Close)
			writeStatusProviders(t, ctx, map[string]config.Provider{
				"provider": {Enabled: true, Backend: config.BackendProxy, BaseURL: upstream.URL + "/v1"},
			})
			if test.name == "unreachable" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				ctx.HTTPHost, ctx.HTTPPort, err = net.SplitHostPort(listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
			}
			ctx.SnapCatalog = &snapcatalog.Reader{}
			ctx.SnapdClient = statusSnapdClient(t, common.InferenceSnapName, true)
			var stdout strings.Builder
			ctx.Stdout = &stdout
			err := execute(Status(ctx))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected stdout: %s", stdout.String())
			}
			if got := upstreamRequests.Load(); got != 0 {
				t.Fatalf("upstream requests = %d, want 0", got)
			}
		})
	}
}

func TestStatusSkipsDaemonWithoutEligibleProviders(t *testing.T) {
	ctx, _, _ := newTestContext(t)
	ctx.HTTPHost = "::invalid"
	ctx.SnapCatalog = &snapcatalog.Reader{}
	writeStatusProviders(t, ctx, map[string]config.Provider{
		"disabled": {Enabled: false, Backend: config.BackendProxy, BaseURL: "http://unused.invalid/v1"},
		"no-url":   {Enabled: true, Backend: config.BackendProxy},
	})
	got, err := (&statusCommand{Context: ctx}).providerHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("health = %v, want nil", got)
	}
}
