package openaiproxy

import (
	"bytes"
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
	lister := &fakeProviders{}
	handler := NewModelsHandler(lister, nil, discardLogger(), false)

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
	if lister.calls.Load() != 0 {
		t.Fatalf("provider list called %d times, want 0", lister.calls.Load())
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
	handler := NewModelsHandler(connectedProviders{root: root}, first.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), false)

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

	handler := NewModelsHandler(&fakeProviders{list: []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}}, upstream.Client(), discardLogger(), false)
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

	handler := NewModelsHandler(&fakeProviders{list: []providers.Provider{
		{Name: "first", BaseURL: first.URL + "/v1"},
		{Name: "second", BaseURL: second.URL + "/v1"},
	}}, first.Client(), discardLogger(), false)

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
	var logs bytes.Buffer
	handler := NewModelsHandler(connectedProviders{root: root}, healthy.Client(), slog.New(slog.NewTextHandler(&logs, nil)), false)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"healthy/ready"`) {
		t.Fatalf("got status %d body %s", response.Code, response.Body.String())
	}
	for _, want := range []string{
		"level=ERROR", `msg="discovering provider models"`, "provider=broken", "error=",
		"request_id=1", "method=GET", "path=/v1/models",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("missing %q:\n%s", want, logs.String())
		}
	}
}

func TestModelsHandlerSkipsProviderWithoutBaseURL(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"ready"}]}`))
	}))
	defer healthy.Close()

	lister := &fakeProviders{list: []providers.Provider{
		{Name: "catalog-only"},
		{Name: "healthy", BaseURL: healthy.URL + "/v1"},
	}}
	handler := NewModelsHandler(lister, healthy.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), false)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"healthy/ready"`) {
		t.Fatalf("got status %d body %s", response.Code, response.Body.String())
	}
}

func TestModelsHandlerRejectsDuplicateProviderNames(t *testing.T) {
	handler := NewModelsHandler(&fakeProviders{list: []providers.Provider{
		{Name: "duplicate", BaseURL: "http://first.example/v1"},
		{Name: "duplicate", BaseURL: "http://second.example/v1"},
	}}, http.DefaultClient, discardLogger(), false)

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
	handler := NewModelsHandler(connectedProviders{root: root}, upstream.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), false)

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
	handler := NewModelsHandler(connectedProviders{root: t.TempDir()}, http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)), false)

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

	lister := &fakeProviders{list: []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}}
	handler := NewModelsHandler(lister, upstream.Client(), discardLogger(), false)
	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	lister.err = errors.New("provider inventory unavailable")
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
	handler := NewModelsHandler(connectedProviders{root: t.TempDir()}, http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)), false)

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
	for _, policy := range []struct {
		name            string
		origin          string
		allowAllOrigins bool
		allowed         bool
	}{
		{name: "loopback", origin: "http://localhost:3000", allowed: true},
		{name: "external rejected", origin: "https://client.example"},
		{name: "allow all", origin: "https://client.example", allowAllOrigins: true, allowed: true},
	} {
		t.Run(policy.name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				for _, test := range []struct {
					name    string
					headers []string
				}{
					{name: "no headers"},
					{name: "empty header list", headers: []string{""}},
					{name: "authorization", headers: []string{"Authorization"}},
					{name: "standard", headers: []string{"Authorization, Content-Type"}},
					{name: "mixed case", headers: []string{"content-type, AUTHORIZATION"}},
					{name: "OpenAI SDK", headers: []string{"authorization, content-type, x-stainless-lang, x-stainless-package-version, x-stainless-os, x-stainless-arch, x-stainless-runtime, x-stainless-runtime-version, x-stainless-retry-count, x-stainless-timeout, x-stainless-helper-method, openai-organization, openai-project"}},
					{name: "custom headers", headers: []string{"X-Custom, OpenAI-Beta"}},
					{name: "multiple header lines", headers: []string{"Authorization, Content-Type", "X-Stainless-Lang, X-Custom"}},
					{name: "token punctuation", headers: []string{"X-!#$%&'*+-.^_`|~0123456789"}},
				} {
					t.Run(method+"/"+test.name, func(t *testing.T) {
						lister := &fakeProviders{}
						handler := NewModelsHandler(lister, http.DefaultClient, discardLogger(), policy.allowAllOrigins)
						request := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
						request.Header.Set("Origin", policy.origin)
						request.Header.Set("Access-Control-Request-Method", method)
						request.Header["Access-Control-Request-Headers"] = test.headers
						response := httptest.NewRecorder()

						handler.ServeHTTP(response, request)

						if lister.calls.Load() != 0 {
							t.Fatal("preflight refreshed provider inventory")
						}
						if !policy.allowed {
							if response.Code != http.StatusForbidden {
								t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
							}
							assertNoCORS(t, response.Header())
							return
						}
						if response.Code != http.StatusNoContent {
							t.Fatalf("preflight returned status %d body %s", response.Code, response.Body.String())
						}
						allowedOrigin := policy.origin
						if policy.allowAllOrigins {
							allowedOrigin = "*"
						}
						assertCORS(t, response.Header(), allowedOrigin)
						wantHeaders := "Authorization, Content-Type"
						if len(test.headers) > 0 {
							wantHeaders = strings.Join(test.headers, ", ")
						}
						if got := response.Header().Get("Access-Control-Allow-Headers"); got != wantHeaders {
							t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, wantHeaders)
						}
						if got := strings.Join(response.Header().Values("Vary"), ", "); got != "Origin, Sec-Fetch-Site, Access-Control-Request-Headers" {
							t.Errorf("Vary = %q, want Origin, Sec-Fetch-Site, Access-Control-Request-Headers", got)
						}
					})
				}
			}
		})
	}
}

func TestModelsHandlerOriginlessRequests(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"id":"native-model"}]}`))
	}))
	defer upstream.Close()

	for _, test := range []struct {
		name            string
		fetchSite       string
		origin          string
		allowAllOrigins bool
		allowed         bool
	}{
		{name: "curl", allowed: true},
		{name: "same origin", fetchSite: "same-origin", allowed: true},
		{name: "same site", fetchSite: "same-site", allowed: true},
		{name: "direct navigation", fetchSite: "none", allowed: true},
		{name: "cross site", fetchSite: "cross-site"},
		{name: "loopback origin with cross-site metadata", fetchSite: "cross-site", origin: "http://localhost:3000", allowed: true},
		{name: "allow all curl", allowAllOrigins: true, allowed: true},
		{name: "allow all cross site", fetchSite: "cross-site", allowAllOrigins: true, allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstreamCalls.Store(0)
			lister := &fakeProviders{list: []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}}
			var logs strings.Builder
			handler := NewModelsHandler(lister, upstream.Client(), slog.New(slog.NewTextHandler(&logs, nil)), test.allowAllOrigins)
			request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if test.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if test.origin == "" {
				assertNoCORS(t, response.Header())
			} else {
				assertCORS(t, response.Header(), test.origin)
			}
			if test.allowed {
				if response.Code != http.StatusOK || lister.calls.Load() != 1 || upstreamCalls.Load() != 1 {
					t.Fatalf("status = %d, inventory calls = %d, upstream calls = %d; want 200, 1, 1", response.Code, lister.calls.Load(), upstreamCalls.Load())
				}
				return
			}
			if response.Code != http.StatusForbidden || lister.calls.Load() != 0 || upstreamCalls.Load() != 0 {
				t.Fatalf("status = %d, inventory calls = %d, upstream calls = %d; want 403, 0, 0", response.Code, lister.calls.Load(), upstreamCalls.Load())
			}
			var body errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Message != "Cross-site requests without an origin are not allowed." || body.Error.Type != "invalid_request_error" {
				t.Errorf("unexpected error: %+v", body.Error)
			}
			if !strings.Contains(logs.String(), `reason="cross-site request without an origin"`) {
				t.Errorf("missing rejection reason in logs: %s", logs.String())
			}
		})
	}
}

func assertCORS(t *testing.T, header http.Header, origin string) {
	t.Helper()
	for name, want := range map[string]string{
		"Access-Control-Allow-Origin":  origin,
		"Access-Control-Allow-Methods": "GET, POST",
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
	if got := header.Get("Vary"); got != "Origin, Sec-Fetch-Site" {
		t.Errorf("Vary = %q, want Origin, Sec-Fetch-Site", got)
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
		{origin: "//localhost", allow: true},
		{origin: "ftp://localhost", allow: true},
		{origin: "ws://127.0.0.1:3000", allow: true},
		{origin: "wss://[::1]:3000", allow: true},
		{origin: "custom://localhost", allow: true},
		{origin: "//client.example"},
		{origin: "ftp://client.example"},
		{origin: "ws://192.168.1.1"},
		{origin: "wss://[::2]"},
		{origin: "custom://client.example"},
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
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Expose-Headers", "*")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				_, _ = w.Write([]byte(`{"data":[{"id":"native-model"}]}`))
			}))
			defer upstream.Close()
			handler := NewModelsHandler(&fakeProviders{list: []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}}, upstream.Client(), discardLogger(), false)

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
	for name, allowAllOrigins := range map[string]bool{
		"default":   false,
		"allow all": true,
	} {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name    string
				origins []string
				methods []string
			}{
				{name: "duplicate origins", origins: []string{"http://localhost", "http://localhost"}, methods: []string{http.MethodGet}},
				{name: "disallowed method", origins: []string{"http://localhost"}, methods: []string{http.MethodDelete}},
				{name: "duplicate methods", origins: []string{"http://localhost"}, methods: []string{http.MethodGet, http.MethodPost}},
			} {
				t.Run(test.name, func(t *testing.T) {
					lister := &fakeProviders{}
					handler := NewModelsHandler(lister, http.DefaultClient, discardLogger(), allowAllOrigins)

					request := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
					request.Header["Origin"] = test.origins
					request.Header["Access-Control-Request-Method"] = test.methods
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
					}
					assertNoCORS(t, response.Header())
					if lister.calls.Load() != 0 {
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
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.Header().Set("Access-Control-Allow-Origin", "https://provider.example")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Expose-Headers", "*")
				_, _ = w.Write([]byte(`{"data":[{"id":"native-model"}]}`))
			}))
			defer upstream.Close()
			handler := NewModelsHandler(&fakeProviders{list: []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}}, upstream.Client(), discardLogger(), true)
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
			message: "Origin is not allowed.",
			reason:  "origin not allowed: ",
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
			message: "Origin is not allowed.",
			reason:  "origin not allowed: https://client.example",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs strings.Builder
			handler := NewModelsHandler(nil, http.DefaultClient, slog.New(slog.NewTextHandler(&logs, nil)), false)
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
	lister := &fakeProviders{}
	handler := NewModelsHandler(lister, http.DefaultClient, discardLogger(), false)

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
	if lister.calls.Load() != 0 {
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
	if got := header.Get("Vary"); got != "Origin, Sec-Fetch-Site" {
		t.Errorf("Vary = %q, want Origin, Sec-Fetch-Site", got)
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

type fakeProviders struct {
	providers.Service
	list  []providers.Provider
	err   error
	calls atomic.Int32
}

func (f *fakeProviders) List(context.Context) ([]providers.Provider, error) {
	f.calls.Add(1)
	return f.list, f.err
}

type connectedProviders struct {
	providers.Service
	root string
}

func (c connectedProviders) List(context.Context) ([]providers.Provider, error) {
	list, err := providers.ConnectedSnapProviders(c.root)
	for i := range list {
		list[i].State = providers.StateEnabled
	}
	return list, err
}
