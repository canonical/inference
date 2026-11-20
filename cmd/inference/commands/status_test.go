package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestStatusReportsMisconfiguredProvidersAsError(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		env      string
	}{
		{name: "missing base URL", provider: "socket-only", env: "SNAP_NAME=socket-only\nUNIX_SOCKET=x.sock\n"},
		{name: "invalid base URL", provider: "bad-url", env: "SNAP_NAME=bad-url\nOPENAI_BASE_URL=ftp://example.com\n"},
		{name: "missing snap name", provider: "no-name", env: "OPENAI_BASE_URL=http://localhost:8081/v1\n"},
		{name: "unreadable env file", provider: "unreadable", env: "SNAP_NAME='unterminated\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			providerDir := filepath.Join(root, tt.provider)
			if err := os.MkdirAll(providerDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(providerDir, "provider.env"), []byte(tt.env), 0o644); err != nil {
				t.Fatal(err)
			}
			client, _ := snapd.NewFakeServer(t, map[string]string{tt.provider: snapd.SnapStatusActive})
			cmd := statusCommand{Context: &common.Context{
				SnapdClient:        client,
				SnapCatalog:        &snapcatalog.Reader{},
				ShareProvidersPath: root,
			}}

			health, problems, err := cmd.providerStatus(context.Background())
			if err != nil {
				t.Fatalf("providerStatus: %v", err)
			}
			if health[tt.provider] != "error" {
				t.Fatalf("health = %q, want %q", health[tt.provider], "error")
			}
			wantPath := filepath.Join(providerDir, "provider.env")
			if len(problems) != 1 || !strings.Contains(problems[0].Error(), wantPath) ||
				!strings.Contains(problems[0].Error(), tt.provider) {
				t.Fatalf("problems = %q, want one naming provider %q and file %q", problems, tt.provider, wantPath)
			}
		})
	}
}

func TestStatusPrintsProviderProblemsToStderrOnlyInVerboseMode(t *testing.T) {
	root := t.TempDir()
	providerDir := filepath.Join(root, "no-name")
	if err := os.MkdirAll(providerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(providerDir, "provider.env")
	if err := os.WriteFile(envPath, []byte("OPENAI_BASE_URL=http://localhost:8081/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantProblem := fmt.Sprintf("Error: provider %q: %s: SNAP_NAME is empty\n", "no-name", envPath)

	for _, format := range []string{"yaml", "json"} {
		for _, verbose := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s verbose=%t", format, verbose), func(t *testing.T) {
				t.Setenv("SNAP", "")
				t.Setenv("SNAP_INSTANCE_NAME", "")
				ctx, stdout, stderr := newTestContext()
				ctx.SnapdClient = statusSnapdClient(t, common.InferenceSnapName, true)
				ctx.SnapCatalog = &snapcatalog.Reader{}
				ctx.ShareProvidersPath = root
				var combined bytes.Buffer
				ctx.Stdout = io.MultiWriter(stdout, &combined)
				ctx.Stderr = io.MultiWriter(stderr, &combined)

				args := []string{"--format=" + format}
				if verbose {
					args = append(args, "--verbose")
				}
				if err := execute(Status(ctx), args...); err != nil {
					t.Fatal(err)
				}

				if strings.Contains(stdout.String(), "warnings") || strings.Contains(stdout.String(), "SNAP_NAME is empty") {
					t.Errorf("stdout = %q, want no provider problem details", stdout.String())
				}
				if !strings.Contains(stdout.String(), "no-name") || !strings.Contains(stdout.String(), "error") {
					t.Errorf("stdout = %q, want the provider reported as error", stdout.String())
				}
				if !verbose {
					if stderr.Len() != 0 {
						t.Errorf("stderr = %q, want empty without --verbose", stderr.String())
					}
					return
				}
				if stderr.String() != wantProblem {
					t.Errorf("stderr = %q, want %q", stderr.String(), wantProblem)
				}
				if !strings.HasPrefix(combined.String(), wantProblem) {
					t.Errorf("output = %q, want the problem printed before the status", combined.String())
				}
			})
		}
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
