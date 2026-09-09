package openaiproxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/canonical/inference/internal/providers"
)

func TestModelsHandlerRefreshesAndAggregatesProviders(t *testing.T) {
	var firstRequests atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("got path %q, want /v1/models", r.URL.Path)
		}
		request := firstRequests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "shared", "object": "model", "created": request, "owned_by": "upstream"},
				{"id": "first-only", "object": "model"},
			},
		})
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "shared"}, {"id": "second-only"}},
		})
	}))
	defer second.Close()

	root := t.TempDir()
	writeProvider(t, root, "z-provider", "zeta", first.URL+"/v1")
	writeProvider(t, root, "a-provider", "alpha", second.URL+"/v1/")
	handler := NewModelsHandler(connectedProviderLister(root), first.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	for request := int64(1); request <= 2; request++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

		if response.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200: %s", response.Code, response.Body.String())
		}
		var got modelsResponse
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Object != "list" || len(got.Data) != 4 {
			t.Fatalf("got %+v, want four models", got)
		}
		wantIDs := []string{"alpha/second-only", "alpha/shared", "zeta/first-only", "zeta/shared"}
		for i, want := range wantIDs {
			if got.Data[i].ID != want {
				t.Errorf("model %d ID = %q, want %q", i, got.Data[i].ID, want)
			}
			if got.Data[i].Object != "model" {
				t.Errorf("model %d object = %q, want model", i, got.Data[i].Object)
			}
		}
		if got.Data[3].Created != request {
			t.Errorf("refresh %d returned created=%d", request, got.Data[3].Created)
		}
	}
}

func TestModelsHandlerSkipsUnavailableProvider(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"ready"}]}`))
	}))
	defer healthy.Close()

	root := t.TempDir()
	writeProvider(t, root, "healthy", "healthy", healthy.URL+"/v1")
	writeProvider(t, root, "broken", "broken", "http://127.0.0.1:1/v1")
	handler := NewModelsHandler(connectedProviderLister(root), healthy.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"healthy/ready"`) {
		t.Fatalf("got status %d body %s", response.Code, response.Body.String())
	}
}

func TestModelsHandlerSkipsProviderWithoutBaseURL(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"ready"}]}`))
	}))
	defer healthy.Close()

	listProviders := func(context.Context) ([]providers.Provider, error) {
		return []providers.Provider{
			{Name: "catalog-only"},
			{Name: "healthy", BaseURL: healthy.URL + "/v1"},
		}, nil
	}
	handler := NewModelsHandler(listProviders, healthy.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"healthy/ready"`) {
		t.Fatalf("got status %d body %s", response.Code, response.Body.String())
	}
}

func TestModelsHandlerRefreshesProviderFiles(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"new-model"}]}`))
	}))
	defer upstream.Close()

	root := t.TempDir()
	handler := NewModelsHandler(connectedProviderLister(root), upstream.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d before provider was added, want 503", response.Code)
	}

	writeProvider(t, root, "new-provider", "new-provider", upstream.URL+"/v1")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"new-provider/new-model"`) {
		t.Fatalf("got status %d body %s after provider was added", response.Code, response.Body.String())
	}
}

func TestModelsHandlerReturnsServiceUnavailableWithoutHealthyProviders(t *testing.T) {
	handler := NewModelsHandler(connectedProviderLister(t.TempDir()), http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want 503", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("got content type %q", got)
	}
}

func TestModelsHandlerRejectsOtherMethodsAndPaths(t *testing.T) {
	handler := NewModelsHandler(connectedProviderLister(t.TempDir()), http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/models", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST returned status %d and Allow %q", response.Code, response.Header().Get("Allow"))
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown path returned status %d", response.Code)
	}
}

func writeProvider(t *testing.T, root, directory, name, baseURL string) {
	t.Helper()
	providerDirectory := filepath.Join(root, directory)
	if err := os.Mkdir(providerDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "SNAP_NAME=" + name + "\nOPENAI_BASE_URL=" + baseURL + "\n"
	if err := os.WriteFile(filepath.Join(providerDirectory, "provider.env"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func connectedProviderLister(root string) func(context.Context) ([]providers.Provider, error) {
	return func(context.Context) ([]providers.Provider, error) {
		return providers.ConnectedSnapProviders(root)
	}
}
