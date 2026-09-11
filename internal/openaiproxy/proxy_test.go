package openaiproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
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
	clientAuthorization := request.Header.Get("Authorization")
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
	if gotAuthorization != "" {
		t.Errorf("upstream authorization = %q, want empty", gotAuthorization)
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
	for _, sensitive := range []string{"hello", "stream=true", clientAuthorization} {
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

func TestProxyRejectsUnqualifiedModel(t *testing.T) {
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
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"unique"}`)))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "does not exist") {
		t.Fatalf("got status %d body %s", response.Code, response.Body.String())
	}
	if listCalls.Load() != 1 || gotModels.Load() != 1 {
		t.Errorf("got list calls %d and model requests %d, want 1 each", listCalls.Load(), gotModels.Load())
	}
}

func TestProxyQualifiedModelsDoNotCollideWithNativeModelIDs(t *testing.T) {
	providerServer := func(provider, advertisedModel string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": advertisedModel}}})
				return
			}
			var request struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("%s decoding request: %v", provider, err)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"provider": provider, "model": request.Model})
		}))
	}

	first := providerServer("first", "model")
	defer first.Close()
	second := providerServer("second", "first/model")
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
		want  string
	}{
		{model: "first/model", want: `{"model":"model","provider":"first"}`},
		{model: "second/first/model", want: `{"model":"first/model","provider":"second"}`},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		body := `{"model":` + mustJSON(t, test.model) + `}`
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != test.want {
			t.Errorf("model %q got status %d body %s, want %s", test.model, response.Code, response.Body.String(), test.want)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model"}`)))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "does not exist") {
		t.Errorf("unqualified model got status %d body %s", response.Code, response.Body.String())
	}
}

func TestProxyFlushesStreamingResponse(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"streamer"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
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
	if got := response.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
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

func TestProxyRewritesNonJSONModelRepresentations(t *testing.T) {
	type upstreamRequest struct {
		path          string
		escapedPath   string
		query         url.Values
		model         string
		file          string
		contentLength int64
	}
	requests := make(chan upstreamRequest, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"native-model"}]}`))
			return
		}

		got := upstreamRequest{
			path:          r.URL.Path,
			escapedPath:   r.URL.EscapedPath(),
			query:         r.URL.Query(),
			contentLength: r.ContentLength,
		}
		switch {
		case strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data"):
			if err := r.ParseMultipartForm(maxProxyRequestSize); err != nil {
				t.Errorf("parsing multipart request: %v", err)
				return
			}
			got.model = r.FormValue("model")
			file, _, err := r.FormFile("audio")
			if err != nil {
				t.Errorf("reading uploaded file: %v", err)
				return
			}
			data, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil {
				t.Errorf("reading uploaded file data: %v", err)
				return
			}
			got.file = string(data)
		case r.Header.Get("Content-Type") == "application/x-www-form-urlencoded":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parsing form request: %v", err)
				return
			}
			got.model = r.Form.Get("model")
		default:
			got.model = r.URL.Query().Get("model")
		}
		requests <- got
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/api/v1?source=proxy"}}, nil
	}, upstream.Client(), discardLogger())
	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Run("URL-encoded form", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/rerank",
			strings.NewReader("model=provider%2Fnative-model&input=keep-me"),
		)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d body %s", response.Code, response.Body.String())
		}
		got := <-requests
		if got.model != "native-model" || got.path != "/api/v1/rerank" {
			t.Errorf("upstream request = %+v", got)
		}
	})

	t.Run("multipart form", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("model", "provider/native-model"); err != nil {
			t.Fatal(err)
		}
		file, err := writer.CreateFormFile("audio", "sample.wav")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, "audio-data"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d body %s", response.Code, response.Body.String())
		}
		got := <-requests
		if got.model != "native-model" || got.file != "audio-data" || got.contentLength <= 0 {
			t.Errorf("upstream request = %+v", got)
		}
	})

	t.Run("WebSocket GET query and escaped path", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodGet,
			"/v1/realtime%2Fsession?model=provider%2Fnative-model&keep=a%2Fb",
			nil,
		)
		request.Header.Set("Connection", "keep-alive, Upgrade")
		request.Header.Set("Upgrade", "websocket")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d body %s", response.Code, response.Body.String())
		}
		got := <-requests
		if got.model != "native-model" ||
			got.path != "/api/v1/realtime/session" ||
			got.escapedPath != "/api/v1/realtime%2Fsession" ||
			got.query.Get("keep") != "a/b" ||
			got.query.Get("source") != "proxy" {
			t.Errorf("upstream request = %+v", got)
		}
	})
}

func TestProxyErrorClassification(t *testing.T) {
	t.Run("client disconnect", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		cancel()
		recorder := httptest.NewRecorder()
		writer := &loggingResponseWriter{ResponseWriter: recorder}

		handleProxyError(writer, request, context.Canceled, discardLogger(), "provider", "http://provider.test/v1", "model")

		if writer.statusCode() != statusClientClosed || writer.WroteHeader() || recorder.Body.Len() != 0 {
			t.Fatalf("status = %d, wrote header = %v, body = %q", writer.statusCode(), writer.WroteHeader(), recorder.Body.String())
		}
	})

	t.Run("server cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		cancel(http.ErrServerClosed)
		recorder := httptest.NewRecorder()
		writer := &loggingResponseWriter{ResponseWriter: recorder}

		handleProxyError(writer, request, context.Canceled, discardLogger(), "provider", "http://provider.test/v1", "model")

		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("upstream failure", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		writer := &loggingResponseWriter{ResponseWriter: recorder}
		var logs bytes.Buffer

		handleProxyError(
			writer,
			httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil),
			errors.New("connection refused"),
			slog.New(slog.NewTextHandler(&logs, nil)),
			"provider",
			"http://user:secret@provider.test/v1?token=secret#backend",
			"model",
		)

		if recorder.Code != http.StatusBadGateway {
			t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
		}
		logOutput := logs.String()
		if !strings.Contains(logOutput, "provider_url=http://provider.test/v1") {
			t.Errorf("logs do not contain provider URL:\n%s", logOutput)
		}
		for _, sensitive := range []string{"user", "secret", "token", "backend"} {
			if strings.Contains(logOutput, sensitive) {
				t.Errorf("logs contain sensitive provider URL value %q:\n%s", sensitive, logOutput)
			}
		}
	})

	t.Run("started response is not modified", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		writer := &loggingResponseWriter{ResponseWriter: recorder}
		writer.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(writer, "partial stream")

		handleProxyError(
			writer,
			httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil),
			errors.New("unexpected EOF"),
			discardLogger(),
			"provider",
			"http://provider.test/v1",
			"model",
		)

		if recorder.Code != http.StatusAccepted || recorder.Body.String() != "partial stream" {
			t.Fatalf("status = %d body = %q", recorder.Code, recorder.Body.String())
		}
	})
}

func TestProxyRecoversTruncatedUpstreamStream(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := io.NopCloser(strings.NewReader(`{"data":[{"id":"streamer"}]}`))
		header := make(http.Header)
		if request.URL.Path != "/v1/models" {
			body = &failingResponseBody{data: []byte("data: partial\n\n")}
			header.Set("Content-Type", "text/event-stream")
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        header,
			Body:          body,
			ContentLength: -1,
			Request:       request,
		}, nil
	})
	handler := NewModelsHandler(func(context.Context) ([]providers.Provider, error) {
		return []providers.Provider{{Name: "provider", BaseURL: "http://provider.test/v1"}}, nil
	}, &http.Client{Transport: transport}, discardLogger())
	if err := handler.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := server.Client().Post(
		server.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"provider/streamer"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if strings.Contains(string(body), `"error"`) {
		t.Fatalf("proxy appended an error body to stream: %q", body)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type failingResponseBody struct {
	data []byte
}

func (b *failingResponseBody) Read(destination []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, errors.New("upstream stream failed")
	}
	count := copy(destination, b.data)
	b.data = b.data[count:]
	return count, nil
}

func (b *failingResponseBody) Close() error {
	return nil
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
