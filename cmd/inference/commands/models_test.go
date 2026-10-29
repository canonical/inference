package commands

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
)

func modelsDaemon(t *testing.T, status int, body string) *common.Context {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	ctx, _, _ := newTestContext(t)
	var err error
	ctx.HTTPHost, ctx.HTTPPort, err = net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestModelsOutput(t *testing.T) {
	const response = `{"object":"list","data":[
		{"id":"gemma4/gemma4-e2b","object":"model","created":0,"owned_by":"gemma4"},
		{"id":"gemma4/gemma4-e4b","object":"model","created":0,"owned_by":"gemma4"},
		{"id":"openai/gpt4.5","object":"model","created":0,"owned_by":"openai"}
	]}`
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "table",
			format: "table",
			want: "PROVIDER  MODEL\n" +
				"gemma4    gemma4-e2b\n" +
				"          gemma4-e4b\n" +
				"openai    gpt4.5\n",
		},
		{
			name:   "json",
			format: "json",
			want: "{\n" +
				"  \"models\": [\n" +
				"    {\n" +
				"      \"id\": \"gemma4/gemma4-e2b\",\n" +
				"      \"provider\": \"gemma4\",\n" +
				"      \"model\": \"gemma4-e2b\"\n" +
				"    },\n" +
				"    {\n" +
				"      \"id\": \"gemma4/gemma4-e4b\",\n" +
				"      \"provider\": \"gemma4\",\n" +
				"      \"model\": \"gemma4-e4b\"\n" +
				"    },\n" +
				"    {\n" +
				"      \"id\": \"openai/gpt4.5\",\n" +
				"      \"provider\": \"openai\",\n" +
				"      \"model\": \"gpt4.5\"\n" +
				"    }\n" +
				"  ]\n" +
				"}\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := modelsDaemon(t, http.StatusOK, response)
			var stdout, stderr strings.Builder
			ctx.Stdout, ctx.Stderr = &stdout, &stderr
			if err := execute(Models(ctx), "--format="+test.format); err != nil {
				t.Fatal(err)
			}
			got := stdout.String()
			if got != test.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, test.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestModelsCommandWithoutProviders(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "table", want: "PROVIDER  MODEL\n"},
		{name: "json", args: []string{"--format=json"}, want: "{\n  \"models\": []\n}\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := modelsDaemon(t, http.StatusOK, `{"object":"list","data":[]}`)
			var stdout, stderr strings.Builder
			ctx.Stdout, ctx.Stderr = &stdout, &stderr
			err := execute(Models(ctx), test.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stdout.String() != test.want {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no output on stderr, got %q", stderr.String())
			}
		})
	}
}

func TestModelsRejectsArguments(t *testing.T) {
	if err := execute(Models(&common.Context{}), "unexpected"); err == nil {
		t.Fatal("expected arguments to be rejected")
	}
}

func TestModelsRejectsInvalidFormat(t *testing.T) {
	for _, format := range []string{"xml", "yaml"} {
		if err := execute(Models(&common.Context{}), "--format="+format); err == nil {
			t.Fatalf("expected %s format to be rejected", format)
		}
	}
}

func TestModelsEmptyOutput(t *testing.T) {
	cmd := &modelsCommand{}
	for _, format := range []string{"table", "json"} {
		var got string
		var err error
		if format == "json" {
			got, err = cmd.outputJSON(nil)
		} else {
			got, err = cmd.outputTable(nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		want := "PROVIDER  MODEL\n"
		if format == "json" {
			want = "{\n  \"models\": []\n}\n"
		}
		if got != want {
			t.Fatalf("%s output = %q, want %q", format, got, want)
		}
	}
}

func TestModelsDaemonErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "unavailable", status: http.StatusServiceUnavailable, body: `{"error":{"message":"No inference providers are available."}}`, want: "returned 503 Service Unavailable"},
		{name: "invalid json", status: http.StatusOK, body: `{`, want: "decoding daemon models"},
		{name: "missing data", status: http.StatusOK, body: `{}`, want: "missing data array"},
		{name: "null data", status: http.StatusOK, body: `{"data":null}`, want: "missing data array"},
		{name: "invalid data", status: http.StatusOK, body: `{"data":{}}`, want: "decoding daemon models"},
		{name: "unqualified ID", status: http.StatusOK, body: `{"data":[{"id":"model","owned_by":"provider"}]}`, want: "invalid model ID"},
		{name: "missing provider", status: http.StatusOK, body: `{"data":[{"id":"provider/model"}]}`, want: "invalid model ID"},
		{name: "mismatched provider", status: http.StatusOK, body: `{"data":[{"id":"other/model","owned_by":"provider"}]}`, want: "invalid model ID"},
		{name: "empty native ID", status: http.StatusOK, body: `{"data":[{"id":"provider/","owned_by":"provider"}]}`, want: "invalid model ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := modelsDaemon(t, test.status, test.body)
			var stdout strings.Builder
			ctx.Stdout = &stdout
			err := execute(Models(ctx))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected stdout: %q", stdout.String())
			}
		})
	}
}

func TestModelsPreservesNativeIDWithSlashes(t *testing.T) {
	ctx := modelsDaemon(t, http.StatusOK, `{"data":[{"id":"provider/organization/model","owned_by":"provider"}]}`)
	var stdout strings.Builder
	ctx.Stdout = &stdout
	if err := execute(Models(ctx), "--format=json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"model": "organization/model"`) {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestModelsDaemonUnreachable(t *testing.T) {
	ctx, _, _ := newTestContext(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx.HTTPHost, ctx.HTTPPort, err = net.SplitHostPort(listener.Addr().String())
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	listener.Close()
	if err := execute(Models(ctx)); err == nil || !strings.Contains(err.Error(), "requesting models from daemon") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModelsRespectsContextCancellation(t *testing.T) {
	ctx := modelsDaemon(t, http.StatusOK, `{"data":[]}`)
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := Models(ctx)
	cmd.SetContext(requestCtx)
	if err := execute(cmd); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
