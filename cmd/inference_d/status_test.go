package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/inference/cmd/inference/commands"
	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/openaiproxy"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/canonical/inference/internal/status"
)

func TestStatusEndpoint(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	requests := 0
	collect := func(ctx context.Context) (status.Status, error) {
		if ctx.Err() != nil {
			return status.Status{}, ctx.Err()
		}
		requests++
		health := "ok"
		if requests > 1 {
			health = "offline"
		}
		return status.Status{
			Services: map[string]string{"inference.d": "active"},
			BaseURL:  "http://[::1]:8401/v1",
			Health:   map[string]string{"provider": health},
			Warnings: []string{"warning"},
		}, nil
	}
	handler := openaiproxy.NewModelsHandler(newStatusHandler(collect, logger), func(context.Context) ([]providers.Provider, error) {
		t.Error("status request queried the model inventory")
		return nil, errors.New("unavailable")
	}, nil, logger)

	for i, path := range []string{"/v1", "/v1/"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("status = %d, headers = %v, body = %s", response.Code, response.Header(), response.Body.String())
		}
		want := fmt.Sprintf(`{"services":{"inference.d":"active"},"proxy":{"openai":{"base-url":"http://[::1]:8401/v1"}},"health":{"provider":%q},"warnings":["warning"]}`+"\n", []string{"ok", "offline"}[i])
		if response.Body.String() != want {
			t.Fatalf("body = %s, want %s", response.Body.String(), want)
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatal("missing CORS headers")
		}
	}
	if requests != 2 {
		t.Fatalf("collected status %d times, want 2", requests)
	}
	for _, path := range []string{"/v1", "/v1/"} {
		if !strings.Contains(logs.String(), "path="+path+" status=200") {
			t.Errorf("missing request log for %s: %s", path, logs.String())
		}
	}
}

func TestStatusEndpointMethods(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := openaiproxy.NewModelsHandler(newStatusHandler(func(context.Context) (status.Status, error) {
		t.Error("unexpected status collection")
		return status.Status{}, nil
	}, logger), nil, nil, logger)

	for _, path := range []string{"/v1", "/v1/"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead, http.MethodOptions} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
				t.Fatalf("%s %s: status = %d, headers = %v", method, path, response.Code, response.Header())
			}
			if !strings.Contains(response.Body.String(), `"type":"invalid_request_error"`) {
				t.Fatalf("unexpected error body: %s", response.Body.String())
			}
		}

		request := httptest.NewRequest(http.MethodOptions, path, nil)
		request.Header.Set("Origin", "https://client.example")
		request.Header.Set("Access-Control-Request-Method", http.MethodGet)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("preflight %s: status = %d, headers = %v", path, response.Code, response.Header())
		}
	}
}

func TestStatusEndpointCollectionFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler := openaiproxy.NewModelsHandler(newStatusHandler(func(got context.Context) (status.Status, error) {
		if got != ctx {
			t.Error("collector did not receive request context")
		}
		return status.Status{}, errors.New("snapd unavailable")
	}, logger), nil, nil, logger)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1", nil).WithContext(ctx))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	want := "{\"error\":{\"message\":\"Inference status is unavailable.\",\"type\":\"service_unavailable\"}}\n"
	if response.Body.String() != want {
		t.Fatalf("body = %s, want %s", response.Body.String(), want)
	}
	if !strings.Contains(logs.String(), "snapd unavailable") {
		t.Fatalf("missing collection error log: %s", logs.String())
	}
}

func TestStatusEndpointMatchesCLI(t *testing.T) {
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_INSTANCE_NAME", "inference_gpu")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"id":"ready"}]}`)
	}))
	defer upstream.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "provider")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provider.env"), []byte("SNAP_NAME=provider\nOPENAI_BASE_URL="+upstream.URL+"/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/apps":
			_, _ = fmt.Fprint(w, `{"type":"sync","status":"OK","result":[{"snap":"inference_gpu","name":"d","active":true}]}`)
		case "/v2/snaps/provider":
			_, _ = fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"name":"provider","status":"active"}}`)
		default:
			t.Errorf("unexpected snapd path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	client := &snapd.Client{Socket: socket}
	catalog := &snapcatalog.Reader{}
	var stdout bytes.Buffer
	command := commands.Status(&common.Context{
		Stdout: &stdout, Stderr: io.Discard,
		SnapdClient: client, SnapCatalog: catalog, ShareProvidersPath: root,
		HTTPHost: "::1", HTTPPort: "8401",
	})
	command.SetArgs([]string{"--format=json"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	collector := status.Collector{
		SnapdClient: client, SnapCatalog: catalog, ShareProvidersPath: root,
		BaseURL: "http://[::1]:8401/v1",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := openaiproxy.NewModelsHandler(newStatusHandler(collector.Collect, logger), collector.ListProviders, nil, logger)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var cli, httpOutput map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &cli); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &httpOutput); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cli, httpOutput) {
		t.Fatalf("HTTP output = %v, CLI output = %v", httpOutput, cli)
	}
	if _, ok := httpOutput["warnings"]; ok {
		t.Fatal("empty warnings must be omitted")
	}
	if !strings.Contains(response.Body.String(), `"health":{"provider":"ok"}`) {
		t.Fatalf("missing provider health: %s", response.Body.String())
	}
}

func TestStatusEndpointEmptyHealth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := openaiproxy.NewModelsHandler(newStatusHandler(func(context.Context) (status.Status, error) {
		return status.Status{Services: map[string]string{"inference.d": "active"}, BaseURL: "http://127.0.0.1:8400/v1"}, nil
	}, logger), nil, nil, logger)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1", nil))
	want := "{\"services\":{\"inference.d\":\"active\"},\"proxy\":{\"openai\":{\"base-url\":\"http://127.0.0.1:8400/v1\"}}}\n"
	if response.Code != http.StatusOK || response.Body.String() != want {
		t.Fatalf("status = %d, body = %s, want %s", response.Code, response.Body.String(), want)
	}
}
