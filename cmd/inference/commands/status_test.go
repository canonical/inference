package commands

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
			ctx, stdout, stderr := newTestContext()
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
	ctx, stdout, _ := newTestContext()
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

func TestStatusPrintsHintWhenProvidersAreRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "provider-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	providerDir := filepath.Join(root, "provider-2")
	if err := os.Mkdir(providerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "provider.env"), []byte("SNAP_NAME=gemma4\nUNIX_SOCKET=x.sock\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Setenv("SNAP", "")
			t.Setenv("SNAP_INSTANCE_NAME", "")
			ctx, stdout, stderr := newTestContext()
			ctx.SnapdClient = statusSnapdClient(t, common.InferenceSnapName, true)
			ctx.SnapCatalog = &snapcatalog.Reader{}
			ctx.ShareProvidersPath = root
			if err := execute(Status(ctx), "--format="+format); err != nil {
				t.Fatal(err)
			}

			for _, unwanted := range []string{"gemma4", "provider-1", "provider-2", "Cannot use", "warnings", "OPENAI_BASE_URL"} {
				if strings.Contains(stdout.String(), unwanted) {
					t.Errorf("stdout = %q, want no provider problem details (found %q)", stdout.String(), unwanted)
				}
			}
			wantHint := "\nHint: cannot use some providers, run \"inference debug providers\" for details.\n"
			if stderr.String() != wantHint {
				t.Errorf("stderr = %q, want %q", stderr.String(), wantHint)
			}
		})
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
