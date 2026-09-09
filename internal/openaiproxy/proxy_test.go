package openaiproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/canonical/inference/internal/providers"
)

func TestProxyUsesStartupSnapshotAndRewritesQualifiedModel(t *testing.T) {
	var listCalls atomic.Int32
	var gotMethod, gotPath, gotQuery, gotModel, gotAuthorization, gotForwardedFor string
	var logs bytes.Buffer
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"native-model"}]}`))
		case "/api/v1/chat/completions":
			gotMethod = r.Method
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			gotAuthorization = r.Header.Get("Authorization")
			gotForwardedFor = r.Header.Get("X-Forwarded-For")
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding request: %v", err)
				return
			}
			if err := json.Unmarshal(body["model"], &gotModel); err != nil {
				t.Errorf("decoding model: %v", err)
				return
			}
			if string(body["messages"]) != `[{"role":"user","content":"hello"}]` {
				t.Errorf("messages changed to %s", body["messages"])
			}
			w.Header().Set("X-Upstream", "reached")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	listProviders := func(context.Context) ([]providers.Provider, error) {
		listCalls.Add(1)
		return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/api/v1?source=proxy"}}, nil
	}
	handler := NewModelsHandler(
		listProviders,
		upstream.Client(),
		slog.New(slog.NewTextHandler(&logs, nil)),
	)
	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions?stream=true",
		strings.NewReader(`{"model":"provider/native-model","messages":[{"role":"user","content":"hello"}]}`),
	)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("X-Forwarded-For", "spoofed")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted || response.Header().Get("X-Upstream") != "reached" {
		t.Fatalf("got status %d headers %v body %s", response.Code, response.Header(), response.Body.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/chat/completions" {
		t.Errorf("upstream got %s %s", gotMethod, gotPath)
	}
	if gotQuery != "source=proxy&stream=true" {
		t.Errorf("upstream query = %q", gotQuery)
	}
	if gotModel != "native-model" {
		t.Errorf("upstream model = %q", gotModel)
	}
	if gotAuthorization != "Bearer secret" {
		t.Errorf("upstream authorization = %q", gotAuthorization)
	}
	if strings.Contains(gotForwardedFor, "spoofed") {
		t.Errorf("upstream X-Forwarded-For trusted inbound value: %q", gotForwardedFor)
	}
	if listCalls.Load() != 1 {
		t.Errorf("provider list called %d times, want 1", listCalls.Load())
	}
	logOutput := logs.String()
	for _, expected := range []string{
		`msg="refreshed provider models"`,
		`msg="routing inference request"`,
		"provider=provider",
		"model=provider/native-model",
		`msg="handled HTTP request"`,
		"status=202",
	} {
		if !strings.Contains(logOutput, expected) {
			t.Errorf("logs do not contain %q:\n%s", expected, logOutput)
		}
	}
	for _, sensitive := range []string{"hello", "stream=true", gotAuthorization} {
		if strings.Contains(logOutput, sensitive) {
			t.Errorf("logs contain sensitive value %q:\n%s", sensitive, logOutput)
		}
	}
}

func TestRedactURLErrorOmitsURLCredentialsAndQuery(t *testing.T) {
	err := &url.Error{
		Op:  "Post",
		URL: "http://user:password@example.com/v1/chat/completions?token=secret",
		Err: errors.New("connection refused"),
	}

	got := redactURLError(err).Error()

	if got != "Post: connection refused" {
		t.Fatalf("redacted error = %q", got)
	}
}

func TestProxyDoesNotRefreshWithoutSnapshot(t *testing.T) {
	var listCalls atomic.Int32
	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		listCalls.Add(1)
		return nil, nil
	}, http.DefaultClient, discardLogger())

	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"anything"}`)),
	)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want 503", response.Code)
	}
	if listCalls.Load() != 0 {
		t.Fatalf("provider list called %d times, want 0", listCalls.Load())
	}
}

func TestProxyUsesExistingSnapshotAndUniqueUnqualifiedModel(t *testing.T) {
	var listCalls atomic.Int32
	var gotModels atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			gotModels.Add(1)
			_, _ = w.Write([]byte(`{"data":[{"id":"unique"}]}`))
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer upstream.Close()

	listProviders := func(context.Context) ([]providers.Provider, error) {
		listCalls.Add(1)
		return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}, nil
	}
	handler := NewModelsHandler(listProviders, upstream.Client(), discardLogger())

	modelsResponse := httptest.NewRecorder()
	handler.ServeHTTP(modelsResponse, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	for range 2 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"unique"}`)))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model":"unique"`) {
			t.Fatalf("got status %d body %s", response.Code, response.Body.String())
		}
	}
	if listCalls.Load() != 1 || gotModels.Load() != 1 {
		t.Errorf("got list calls %d and model requests %d, want 1 each", listCalls.Load(), gotModels.Load())
	}
}

func TestProxyRejectsAmbiguousAndUnknownModels(t *testing.T) {
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
	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		model string
		code  int
		text  string
	}{
		{model: "shared", code: http.StatusBadRequest, text: "ambiguous"},
		{model: "missing", code: http.StatusNotFound, text: "does not exist"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		body := `{"model":` + mustJSON(t, test.model) + `}`
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
		if response.Code != test.code || !strings.Contains(response.Body.String(), test.text) {
			t.Errorf("model %q got status %d body %s", test.model, response.Code, response.Body.String())
		}
	}
}

func TestProxyFlushesStreamingResponse(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"streamer"}]}`))
			return
		}
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		<-release
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	defer upstream.Close()

	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}, nil
	}, upstream.Client(), discardLogger())
	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	response, err := proxyServer.Client().Post(
		proxyServer.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"provider/streamer","stream":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("first streamed line = %q, %v", line, err)
	}
	close(release)
	remaining, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(remaining), "data: second") {
		t.Fatalf("remaining stream = %q, %v", remaining, err)
	}
}

func modelServer(t *testing.T, model string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected proxy request to %s", r.URL.Path)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": model}}})
	}))
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
