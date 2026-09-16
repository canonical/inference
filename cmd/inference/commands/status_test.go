package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
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
			want: "services:\n  proxy: active\nproxy:\n  openai:\n    base-url: unavailable\n",
		},
		{
			name: "json",
			args: []string{"--format=json"},
			want: "{\n  \"services\": {\n    \"proxy\": \"active\"\n  },\n  \"proxy\": {\n    \"openai\": {\n      \"base-url\": \"unavailable\"\n    }\n  }\n}\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SNAP", "")
			t.Setenv("SNAP_INSTANCE_NAME", "")
			ctx, stdout, stderr := newTestContext()
			ctx.SnapdClient = statusSnapdClient(t, inferenceSnapName, true)
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
	ctx, _, _ := newTestContext()
	ctx.SnapdClient = statusSnapdClient(t, instanceName, true)
	ctx.SnapCatalog = &snapcatalog.Reader{}

	if err := execute(Status(ctx)); err != nil {
		t.Fatal(err)
	}
}

func TestProxyOpenAIBaseURL(t *testing.T) {
	t.Setenv("SNAP", "/snap/inference/current")
	t.Setenv("SNAP_NAME", inferenceSnapName)
	binDir := t.TempDir()
	snapctlPath := filepath.Join(binDir, "snapctl")
	snapctl := `#!/bin/sh
case "$2" in
  http.host) printf '%s\n' '::1' ;;
  http.port) printf '%s\n' '8401' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(snapctlPath, []byte(snapctl), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := proxyOpenAIBaseURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://[::1]:8401/v1" {
		t.Fatalf("got %q, want %q", got, "http://[::1]:8401/v1")
	}
}

func TestProxyOpenAIBaseURLOutsideSnap(t *testing.T) {
	t.Setenv("SNAP", "")

	got, err := proxyOpenAIBaseURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "unavailable" {
		t.Fatalf("got %q, want %q", got, "unavailable")
	}
}

func TestSnapConfigurationValuePreservesSnapctlError(t *testing.T) {
	binDir := t.TempDir()
	snapctlPath := filepath.Join(binDir, "snapctl")
	snapctl := `#!/bin/sh
printf '%s\n' 'error: snapctl: cannot invoke snapctl operation commands (here "get") from outside of a snap' >&2
exit 1
`
	if err := os.WriteFile(snapctlPath, []byte(snapctl), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := snapConfigurationValue(context.Background(), "http.host")
	if err == nil {
		t.Fatal("expected snapctl error")
	}
	want := `error: snapctl: cannot invoke snapctl operation commands (here "get") from outside of a snap`
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err, want)
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
