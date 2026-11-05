package openaiproxy

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/status"
)

func TestModelsHandlerStatus(t *testing.T) {
	for _, path := range []string{"/v1", "/v1/"} {
		t.Run(path, func(t *testing.T) {
			for _, test := range []struct {
				name   string
				result status.Status
				want   string
			}{
				{
					name: "populated",
					result: status.Status{
						Services: map[string]string{"inference.d": "active"},
						Proxy: map[string]map[string]string{
							"openai": {"base-url": "http://127.0.0.1:8400/v1"},
						},
						Health:   map[string]string{"provider": "ok"},
						Warnings: []string{"GPU unavailable"},
					},
					want: "{\"services\":{\"inference.d\":\"active\"},\"proxy\":{\"openai\":{\"base-url\":\"http://127.0.0.1:8400/v1\"}},\"health\":{\"provider\":\"ok\"},\"warnings\":[\"GPU unavailable\"]}\n",
				},
				{
					name:   "optional fields omitted",
					result: status.Status{Services: map[string]string{"inference.d": "inactive"}},
					want:   "{\"services\":{\"inference.d\":\"inactive\"}}\n",
				},
			} {
				t.Run(test.name, func(t *testing.T) {
					calls := 0
					lookupStatus := func(ctx context.Context) (status.Status, error) {
						calls++
						if ctx != t.Context() {
							t.Error("status lookup did not receive request context")
						}
						return test.result, nil
					}
					handler := NewModelsHandler(nil, nil, discardLogger(), lookupStatus)
					response := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(t.Context())
					handler.ServeHTTP(response, request)

					if response.Code != http.StatusOK {
						t.Fatalf("got status %d, want 200: %s", response.Code, response.Body.String())
					}
					if got := response.Header().Get("Content-Type"); got != "application/json" {
						t.Errorf("got content type %q, want application/json", got)
					}
					if got := response.Body.String(); got != test.want {
						t.Errorf("got body %q, want %q", got, test.want)
					}
					if calls != 1 {
						t.Errorf("got %d status lookups, want 1", calls)
					}
					assertPermissiveCORS(t, response.Header())
				})
			}
		})
	}
}

func TestModelsHandlerStatusUnavailable(t *testing.T) {
	for _, test := range []struct {
		name   string
		lookup func(context.Context) (status.Status, error)
		log    string
	}{
		{
			name: "lookup failure",
			lookup: func(context.Context) (status.Status, error) {
				return status.Status{}, errors.New("snapd failure details")
			},
			log: "snapd failure details",
		},
		{
			name: "not configured",
			log:  "status lookup is not configured",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			handler := NewModelsHandler(nil, nil, logger, test.lookup)
			for _, path := range []string{"/v1", "/v1/"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != http.StatusServiceUnavailable {
					t.Fatalf("got status %d, want 503: %s", response.Code, response.Body.String())
				}
				if got := response.Header().Get("Content-Type"); got != "application/json" {
					t.Errorf("got content type %q, want application/json", got)
				}
				want := "{\"error\":{\"message\":\"Inference status is unavailable.\",\"type\":\"service_unavailable\"}}\n"
				if got := response.Body.String(); got != want {
					t.Errorf("got body %q, want %q", got, want)
				}
			}
			if !strings.Contains(logs.String(), test.log) {
				t.Errorf("got logs %q, want %q", logs.String(), test.log)
			}
		})
	}
}

func TestModelsHandlerStatusMethodsAndPaths(t *testing.T) {
	handler := NewModelsHandler(nil, nil, discardLogger(), func(context.Context) (status.Status, error) {
		t.Error("unexpected status lookup")
		return status.Status{}, errors.New("unexpected status lookup")
	})
	for _, test := range []struct {
		method    string
		path      string
		preflight bool
		code      int
		allow     string
	}{
		{method: http.MethodPost, path: "/v1", code: http.StatusMethodNotAllowed, allow: http.MethodGet},
		{method: http.MethodPost, path: "/v1/", code: http.StatusMethodNotAllowed, allow: http.MethodGet},
		{method: http.MethodHead, path: "/v1", code: http.StatusMethodNotAllowed, allow: http.MethodGet},
		{method: http.MethodOptions, path: "/v1", preflight: true, code: http.StatusNoContent},
		{method: http.MethodOptions, path: "/v1/", preflight: true, code: http.StatusNoContent},
		{method: http.MethodGet, path: "/v10", code: http.StatusNotFound},
		{method: http.MethodGet, path: "/v1/unknown", code: http.StatusServiceUnavailable},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.preflight {
				request.Header.Set("Origin", "https://client.example")
				request.Header.Set("Access-Control-Request-Method", http.MethodGet)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.code {
				t.Errorf("got status %d, want %d: %s", response.Code, test.code, response.Body.String())
			}
			if got := response.Header().Get("Allow"); got != test.allow {
				t.Errorf("got Allow %q, want %q", got, test.allow)
			}
			assertPermissiveCORS(t, response.Header())
		})
	}
}
