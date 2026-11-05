package commands

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func statusSnapdClient(t *testing.T, snapName string, active bool) *snapd.Client {
	t.Helper()
	return statusSnapdServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/apps" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("names"); got != snapName+".d" {
			t.Errorf("unexpected names query %q", got)
		}
		fmt.Fprintf(w, `{"type":"sync","status":"OK","result":[{"snap":%q,"name":"d","active":%t}]}`, snapName, active)
	})
}

func statusSnapdServer(t *testing.T, handler http.HandlerFunc) *snapd.Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "snapd.socket")
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
			ctx.SnapdClient = statusSnapdClient(t, config.InferenceSnapName, true)
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

func TestStatusLookupError(t *testing.T) {
	t.Setenv("SNAP_INSTANCE_NAME", "")
	ctx, stdout, _ := newTestContext()
	ctx.SnapdClient = statusSnapdServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"type":"error","status-code":403,"result":{"message":"access denied"}}`)
	})
	err := execute(Status(ctx))
	if err == nil || err.Error() != "access denied. Try again using sudo" {
		t.Fatalf("got error %v, want friendly access denied error", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", stdout.String())
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

func TestStatusOutput(t *testing.T) {
	output := statusOutput{
		Services: map[string]string{"proxy": "active"},
		Proxy: map[string]map[string]string{
			"openai": {"base-url": "http://127.0.0.1:8080/v1"},
		},
		Health: map[string]string{
			"gemma4":      "ok",
			"qwen3.6":     "not responsive",
			"ollama":      "ok",
			"openai-work": "offline",
		},
		Warnings: []string{"Unable to communicate with NVIDIA GeForce RTX 4050 GPU."},
	}

	cmd := &statusCommand{}
	for _, test := range []struct {
		name   string
		encode func(statusOutput) (string, error)
		want   string
	}{
		{
			name:   "json",
			encode: cmd.outputJSON,
			want:   "{\n  \"services\": {\n    \"proxy\": \"active\"\n  },\n  \"proxy\": {\n    \"openai\": {\n      \"base-url\": \"http://127.0.0.1:8080/v1\"\n    }\n  },\n  \"health\": {\n    \"gemma4\": \"ok\",\n    \"ollama\": \"ok\",\n    \"openai-work\": \"offline\",\n    \"qwen3.6\": \"not responsive\"\n  },\n  \"warnings\": [\n    \"Unable to communicate with NVIDIA GeForce RTX 4050 GPU.\"\n  ]\n}\n",
		},
		{
			name:   "yaml",
			encode: cmd.outputYAML,
			want:   "services:\n  proxy: active\nproxy:\n  openai:\n    base-url: http://127.0.0.1:8080/v1\nhealth:\n  gemma4: ok\n  ollama: ok\n  openai-work: offline\n  qwen3.6: not responsive\nwarnings:\n  - Unable to communicate with NVIDIA GeForce RTX 4050 GPU.\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.encode(output)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, test.want)
			}
		})
	}
}

func TestStatusWriteError(t *testing.T) {
	t.Setenv("SNAP_INSTANCE_NAME", "")
	ctx, _, _ := newTestContext()
	ctx.SnapdClient = statusSnapdClient(t, config.InferenceSnapName, true)
	ctx.SnapCatalog = &snapcatalog.Reader{}
	reader, writer := io.Pipe()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx.Stdout = writer
	if err := execute(Status(ctx)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("got error %v, want closed pipe error", err)
	}
}
