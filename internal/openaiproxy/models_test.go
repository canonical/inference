package openaiproxy

import (
	"context"
	"encoding/json"
	"errors"
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

func TestModelsHandlerRetrievesModelFromSnapshot(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("got upstream path %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"organization/model","created":1234}]}`))
	}))
	defer upstream.Close()

	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}, nil
	}, upstream.Client(), discardLogger())
	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/v1/models/provider/organization/model", nil),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("got status %d body %s", response.Code, response.Body.String())
	}
	var got modelOutput
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := modelOutput{
		ID:      "provider/organization/model",
		Object:  "model",
		Created: 1234,
		OwnedBy: "provider",
	}
	if got != want {
		t.Errorf("returned %+v, want %+v", got, want)
	}
	if requests.Load() != 1 {
		t.Errorf("upstream received %d requests, want only the initial model list request", requests.Load())
	}
}

func TestModelsHandlerRejectsUnavailableUnqualifiedAndUnknownModelRetrieval(t *testing.T) {
	first := modelServer(t, "shared")
	defer first.Close()
	second := modelServer(t, "shared")
	defer second.Close()

	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		return []providers.Provider{
			{Name: "first", BaseURL: first.URL + "/v1"},
			{Name: "second", BaseURL: second.URL + "/v1"},
		}, nil
	}, first.Client(), discardLogger())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models/anything", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("request without snapshot got status %d, want 503", response.Code)
	}

	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		method string
		path   string
		code   int
		allow  string
	}{
		{name: "unqualified", method: http.MethodGet, path: "/v1/models/shared", code: http.StatusNotFound},
		{name: "unknown", method: http.MethodGet, path: "/v1/models/missing", code: http.StatusNotFound},
		{name: "empty", method: http.MethodGet, path: "/v1/models/", code: http.StatusNotFound},
		{name: "method", method: http.MethodPost, path: "/v1/models/first/shared", code: http.StatusMethodNotAllowed, allow: http.MethodGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.code || response.Header().Get("Allow") != test.allow {
				t.Errorf("got status %d and Allow %q, want %d and %q", response.Code, response.Header().Get("Allow"), test.code, test.allow)
			}
		})
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
	handler := NewModelsHandler(connectedProviderLister(root), healthy.Client(), discardLogger())

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

func TestModelsHandlerRejectsDuplicateProviderNames(t *testing.T) {
	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		return []providers.Provider{
			{Name: "duplicate", BaseURL: "http://first.example/v1"},
			{Name: "duplicate", BaseURL: "http://second.example/v1"},
		}, nil
	}, http.DefaultClient, discardLogger())

	err := handler.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), `duplicate provider name "duplicate"`) {
		t.Fatalf("got error %v, want duplicate provider name error", err)
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
	if response.Code != http.StatusOK || response.Body.String() != "{\"object\":\"list\",\"data\":[]}\n" {
		t.Fatalf("got status %d body %s before provider was added, want empty model list", response.Code, response.Body.String())
	}

	writeProvider(t, root, "new-provider", "new-provider", upstream.URL+"/v1")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"new-provider/new-model"`) {
		t.Fatalf("got status %d body %s after provider was added", response.Code, response.Body.String())
	}
}

func TestModelsHandlerReturnsEmptyListWithoutAvailableProviders(t *testing.T) {
	handler := NewModelsHandler(connectedProviderLister(t.TempDir()), http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("got content type %q", got)
	}
	if response.Body.String() != "{\"object\":\"list\",\"data\":[]}\n" {
		t.Fatalf("got body %s, want empty model list", response.Body.String())
	}
}

func TestModelsHandlerPreservesSnapshotWhenRefreshFails(t *testing.T) {
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"ready"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"id":"completion"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		if fail.Load() {
			return nil, errors.New("provider inventory unavailable")
		}
		return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}, nil
	}, upstream.Client(), discardLogger())
	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	fail.Store(true)
	if err := handler.Refresh(context.Background()); err == nil {
		t.Fatal("refresh succeeded, want error")
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"provider/ready"}`)),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d body %s, want 200", response.Code, response.Body.String())
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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
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
		list, err := providers.ConnectedSnapProviders(root)
		for i := range list {
			list[i].State = providers.StateEnabled
		}
		return list, err
	}
}
