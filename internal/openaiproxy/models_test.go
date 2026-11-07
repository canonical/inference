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

func TestModelsHandlerServesDefaultResponse(t *testing.T) {
	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		t.Fatal("default response request should not list providers")
		return nil, nil
	}, nil, discardLogger())

	for _, path := range []string{"/", "/v1", "/v1/"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

			if response.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200: %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("got Content-Type %q, want application/json", got)
			}
			if got, want := response.Body.String(), "{\"documentation-url\":\"https://github.com/canonical/inference\"}\n"; got != want {
				t.Errorf("got body %q, want %q", got, want)
			}
		})
	}
}

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
	handler := NewModelsHandler(connectedProviderLister(root), first.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), HandlerOptions{})

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
	}, upstream.Client(), discardLogger(), HandlerOptions{})

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
	}, first.Client(), discardLogger(), HandlerOptions{})

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
	handler := NewModelsHandler(connectedProviderLister(root), healthy.Client(), discardLogger(), HandlerOptions{})

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
	handler := NewModelsHandler(listProviders, healthy.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), HandlerOptions{})

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
	}, http.DefaultClient, discardLogger(), HandlerOptions{})

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
	handler := NewModelsHandler(connectedProviderLister(root), upstream.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), HandlerOptions{})

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
	handler := NewModelsHandler(connectedProviderLister(t.TempDir()), http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)), HandlerOptions{})

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
	}, upstream.Client(), discardLogger(), HandlerOptions{})

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
	handler := NewModelsHandler(connectedProviderLister(t.TempDir()), http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)), HandlerOptions{})

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

func TestModelsHandlerCORSPreflight(t *testing.T) {
	handler := NewModelsHandler(connectedProviderLister(t.TempDir()), http.DefaultClient, discardLogger(), HandlerOptions{})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, headers := range []string{"", "Authorization, Content-Type", "content-type, AUTHORIZATION"} {
			t.Run(method+"/"+headers, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
				request.Header.Set("Origin", "http://localhost:3000")
				request.Header.Set("Access-Control-Request-Method", method)
				if headers != "" {
					request.Header.Set("Access-Control-Request-Headers", headers)
				}
				response := httptest.NewRecorder()

				handler.ServeHTTP(response, request)

				if response.Code != http.StatusNoContent {
					t.Fatalf("preflight returned status %d body %s", response.Code, response.Body.String())
				}
				assertCORS(t, response.Header(), "http://localhost:3000")
			})
		}
	}
}

func assertCORS(t *testing.T, header http.Header, origin string) {
	t.Helper()
	for name, want := range map[string]string{
		"Access-Control-Allow-Origin":  origin,
		"Access-Control-Allow-Methods": "GET, POST",
		"Access-Control-Allow-Headers": "Authorization, Content-Type",
	} {
		if got := header.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want one %q value", name, got, want)
		}
	}
	if got := header.Get("Access-Control-Expose-Headers"); got != "" {
		t.Errorf("Access-Control-Expose-Headers = %q, want empty", got)
	}
	if got := header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty", got)
	}
	if got := header.Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
}

func TestModelsHandlerOriginPolicy(t *testing.T) {
	for _, test := range []struct {
		origin string
		allow  bool
	}{
		{origin: "", allow: true},
		{origin: "http://localhost", allow: true},
		{origin: "https://LOCALHOST:443", allow: true},
		{origin: "http://localhost:3000", allow: true},
		{origin: "http://127.0.0.1:8080", allow: true},
		{origin: "https://127.42.0.1:65535", allow: true},
		{origin: "http://[::1]", allow: true},
		{origin: "https://[::1]:3000", allow: true},
		{origin: "https://client.example"},
		{origin: "http://localhost.evil.example"},
		{origin: "http://evil-localhost"},
		{origin: "http://app.localhost"},
		{origin: "http://192.168.1.1"},
		{origin: "http://0.0.0.0"},
		{origin: "http://[::]"},
		{origin: "http://[::2]"},
		{origin: "http://[::1%25lo]", allow: true},
		{origin: "http://[::2%25lo]"},
		{origin: "null"},
		{origin: "localhost"},
		{origin: "//localhost"},
		{origin: "ftp://localhost"},
		{origin: "http://user@localhost", allow: true},
		{origin: "http://localhost/", allow: true},
		{origin: "http://localhost/path", allow: true},
		{origin: "http://localhost?", allow: true},
		{origin: "http://localhost?query=value", allow: true},
		{origin: "http://localhost#", allow: true},
		{origin: "http://localhost#fragment", allow: true},
		{origin: "http://localhost@client.example"},
		{origin: "http://client.example/localhost"},
		{origin: "http://client.example?host=localhost"},
		{origin: "http://client.example#localhost"},
		{origin: "http://localhost:", allow: true},
		{origin: "http://localhost:invalid"},
		{origin: "http://localhost:65536", allow: true},
		{origin: "http://localhost:-1"},
		{origin: "http://[localhost]"},
		{origin: "http://::1:3000"},
		{origin: "http://localhost https://client.example"},
	} {
		t.Run(test.origin, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Expose-Headers", "*")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				_, _ = w.Write([]byte(`{"data":[{"id":"native-model"}]}`))
			}))
			defer upstream.Close()
			handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
				return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}, nil
			}, upstream.Client(), discardLogger(), HandlerOptions{})

			if err := handler.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			upstreamCalls.Store(0)

			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
				path := "/v1/models"
				if method != http.MethodGet {
					path = "/v1/chat/completions"
				}
				request := httptest.NewRequest(method, path, strings.NewReader(`{"model":"provider/native-model"}`))
				if test.origin != "" {
					request.Header.Set("Origin", test.origin)
				}
				if method == http.MethodOptions {
					request.Header.Set("Access-Control-Request-Method", http.MethodPost)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				wantStatus := http.StatusOK
				if !test.allow {
					wantStatus = http.StatusForbidden
				} else if method == http.MethodOptions && test.origin != "" {
					wantStatus = http.StatusNoContent
				}
				if response.Code != wantStatus {
					t.Fatalf("%s status = %d, want %d: %s", method, response.Code, wantStatus, response.Body.String())
				}
				if test.allow && test.origin != "" {
					assertCORS(t, response.Header(), test.origin)
				} else {
					assertNoCORS(t, response.Header())
				}
			}
			if !test.allow && upstreamCalls.Load() != 0 {
				t.Errorf("rejected origin reached upstream %d times", upstreamCalls.Load())
			}
			if test.allow && upstreamCalls.Load() == 0 {
				t.Error("allowed requests did not reach upstream")
			}
		})
	}
}

func TestModelsHandlerRejectsInvalidCORSRequests(t *testing.T) {
	for name, options := range map[string]HandlerOptions{
		"default":   {},
		"allow all": {AllowAllOrigins: true},
	} {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name    string
				origins []string
				methods []string
				headers []string
			}{
				{name: "duplicate origins", origins: []string{"http://localhost", "http://localhost"}, methods: []string{http.MethodGet}},
				{name: "disallowed method", origins: []string{"http://localhost"}, methods: []string{http.MethodDelete}},
				{name: "duplicate methods", origins: []string{"http://localhost"}, methods: []string{http.MethodGet, http.MethodPost}},
				{name: "disallowed header", origins: []string{"http://localhost"}, methods: []string{http.MethodPost}, headers: []string{"Authorization, X-Custom"}},
				{name: "additional header line", origins: []string{"http://localhost"}, methods: []string{http.MethodPost}, headers: []string{"Content-Type", "X-Custom"}},
				{name: "empty header", origins: []string{"http://localhost"}, methods: []string{http.MethodPost}, headers: []string{""}},
			} {
				t.Run(test.name, func(t *testing.T) {
					var listCalls atomic.Int32
					handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
						listCalls.Add(1)
						return nil, nil
					}, http.DefaultClient, discardLogger(), options)

					request := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
					request.Header["Origin"] = test.origins
					request.Header["Access-Control-Request-Method"] = test.methods
					request.Header["Access-Control-Request-Headers"] = test.headers
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
					}
					assertNoCORS(t, response.Header())
					if listCalls.Load() != 0 {
						t.Error("rejected request refreshed provider inventory")
					}
				})
			}
		})
	}
}

func TestModelsHandlerAllowAllOrigins(t *testing.T) {
	for _, test := range []struct {
		name    string
		origins []string
		allow   bool
	}{
		{name: "no origin", allow: true},
		{name: "loopback", origins: []string{"http://localhost:3000"}, allow: true},
		{name: "external HTTPS", origins: []string{"https://client.example"}, allow: true},
		{name: "external HTTP", origins: []string{"http://192.168.1.1:8080"}, allow: true},
		{name: "IPv6", origins: []string{"https://[2001:db8::1]:443"}, allow: true},
		{name: "opaque", origins: []string{"null"}, allow: true},
		{name: "extension", origins: []string{"chrome-extension://abcdef"}, allow: true},
		{name: "empty", origins: []string{""}, allow: true},
		{name: "multiple", origins: []string{"https://client.example", "http://localhost"}},
		{name: "combined", origins: []string{"https://client.example http://localhost"}, allow: true},
		{name: "arbitrary value", origins: []string{"not-an-origin"}, allow: true},
		{name: "path", origins: []string{"https://client.example/path"}, allow: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				w.Header().Set("Access-Control-Allow-Origin", "https://provider.example")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Expose-Headers", "*")
				_, _ = w.Write([]byte(`{"data":[{"id":"native-model"}]}`))
			}))
			defer upstream.Close()
			handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
				return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}, nil
			}, upstream.Client(), discardLogger(), HandlerOptions{AllowAllOrigins: true})
			if err := handler.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			upstreamCalls.Store(0)

			for _, requestTest := range []struct {
				method     string
				path       string
				wantStatus int
				webSocket  bool
			}{
				{method: http.MethodGet, path: "/v1/models", wantStatus: http.StatusOK},
				{method: http.MethodGet, path: "/v1/models/provider/native-model", wantStatus: http.StatusOK},
				{method: http.MethodPost, path: "/v1/chat/completions", wantStatus: http.StatusOK},
				{method: http.MethodOptions, path: "/v1/chat/completions", wantStatus: http.StatusNoContent},
				{method: http.MethodGet, path: "/v1/realtime?model=provider/native-model", wantStatus: http.StatusOK, webSocket: true},
				{method: http.MethodOptions, path: "/v1/realtime?model=provider/native-model", wantStatus: http.StatusOK, webSocket: true},
				{method: http.MethodGet, path: "/v1/models/missing", wantStatus: http.StatusNotFound},
				{method: http.MethodGet, path: "/missing", wantStatus: http.StatusNotFound},
				{method: http.MethodPost, path: "/v1/models", wantStatus: http.StatusMethodNotAllowed},
			} {
				request := httptest.NewRequest(requestTest.method, requestTest.path, strings.NewReader(`{"model":"provider/native-model"}`))
				request.Header["Origin"] = test.origins
				if requestTest.method == http.MethodOptions {
					request.Header.Set("Access-Control-Request-Method", http.MethodPost)
					request.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
				}
				if requestTest.webSocket {
					request.Header.Set("Connection", "Upgrade")
					request.Header.Set("Upgrade", "websocket")
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				wantStatus := requestTest.wantStatus
				if !test.allow {
					wantStatus = http.StatusForbidden
				} else if request.Header.Get("Origin") == "" && requestTest.method == http.MethodOptions && !requestTest.webSocket {
					wantStatus = http.StatusOK
				}
				if response.Code != wantStatus {
					t.Fatalf("%s %s status = %d, want %d: %s", requestTest.method, requestTest.path, response.Code, wantStatus, response.Body.String())
				}
				if test.allow && len(test.origins) != 0 {
					assertCORS(t, response.Header(), "*")
				} else {
					assertNoCORS(t, response.Header())
				}
			}
			if !test.allow && upstreamCalls.Load() != 0 {
				t.Fatalf("rejected origin reached upstream %d times", upstreamCalls.Load())
			}
			if test.allow && upstreamCalls.Load() == 0 {
				t.Fatal("allowed requests did not reach upstream")
			}
		})
	}
}

func TestModelsHandlerOriginErrorMessages(t *testing.T) {
	for _, test := range []struct {
		name    string
		origins []string
		message string
		reason  string
	}{
		{
			name:    "empty origin",
			origins: []string{""},
			message: "The request origin must be an HTTP/HTTPS loopback origin.",
			reason:  "origin is not an HTTP/HTTPS loopback origin",
		},
		{
			name:    "multiple origins",
			origins: []string{"http://localhost", "https://client.example"},
			message: "Multiple request origins are not allowed.",
			reason:  "multiple origins are not allowed",
		},
		{
			name:    "non-loopback origin",
			origins: []string{"https://client.example"},
			message: "The request origin must be an HTTP/HTTPS loopback origin.",
			reason:  "origin is not an HTTP/HTTPS loopback origin",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs strings.Builder
			handler := NewModelsHandler(nil, http.DefaultClient, slog.New(slog.NewTextHandler(&logs, nil)), HandlerOptions{})
			request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			request.Header["Origin"] = test.origins
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", response.Code)
			}
			var body errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Message != test.message || body.Error.Type != "invalid_request_error" {
				t.Errorf("error = %+v, want message %q and type invalid_request_error", body.Error, test.message)
			}
			if !strings.Contains(logs.String(), `reason="`+test.reason+`"`) {
				t.Errorf("logs = %q, want reason %q", logs.String(), test.reason)
			}
			assertNoCORS(t, response.Header())
		})
	}
}

func TestModelsHandlerRejectsExternalWebSocketOrigin(t *testing.T) {
	var listCalls atomic.Int32
	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		listCalls.Add(1)
		return nil, nil
	}, http.DefaultClient, discardLogger(), HandlerOptions{})

	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		request := httptest.NewRequest(method, "/v1/realtime?model=provider/native-model", nil)
		request.Header.Set("Origin", "https://client.example")
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
		if method == http.MethodOptions {
			request.Header.Set("Access-Control-Request-Method", http.MethodGet)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", method, response.Code)
		}
		assertNoCORS(t, response.Header())
	}
	if listCalls.Load() != 0 {
		t.Error("rejected WebSocket request refreshed provider inventory")
	}
}

func assertNoCORS(t *testing.T, header http.Header) {
	t.Helper()
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("unexpected CORS header %s = %q", name, header.Values(name))
		}
	}
	if got := header.Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
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
