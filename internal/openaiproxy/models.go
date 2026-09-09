package openaiproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/canonical/inference/internal/providers"
)

const (
	maxModelsResponseSize = 4 << 20
	maxProxyRequestSize   = 16 << 20
	modelDiscoveryTimeout = 15 * time.Second
	statusClientClosed    = 499
)

type ModelsHandler struct {
	listProviders func(context.Context) ([]providers.Provider, error)
	client        *http.Client
	logger        *slog.Logger
	refreshMu     sync.Mutex
	snapshot      atomic.Pointer[routingSnapshot]
	requestID     atomic.Uint64
}

type upstreamModelsResponse struct {
	Data []upstreamModel `json:"data"`
}

type upstreamModel struct {
	ID      string `json:"id"`
	Created int64  `json:"created,omitempty"`
}

type modelsResponse struct {
	Object string        `json:"object"`
	Data   []modelOutput `json:"data"`
}

type modelOutput struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type errorResponse struct {
	Error errorOutput `json:"error"`
}

type errorOutput struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type providerModels struct {
	provider providers.Provider
	models   []upstreamModel
	err      error
	duration time.Duration
}

type modelRoute struct {
	providerName  string
	baseURL       string
	nativeModelID string
	model         modelOutput
}

type routingSnapshot struct {
	models    []modelOutput
	routes    map[string]modelRoute
	ambiguous map[string]struct{}
}

func NewModelsHandler(
	listProviders func(context.Context) ([]providers.Provider, error),
	client *http.Client,
	logger *slog.Logger,
) *ModelsHandler {
	if client == nil {
		client = http.DefaultClient
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ModelsHandler{listProviders: listProviders, client: client, logger: logger}
}

func (h *ModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := h.requestID.Add(1)
	logger := h.logger.With(
		"request_id", requestID,
		"method", r.Method,
		"path", r.URL.Path,
	)
	responseWriter := &loggingResponseWriter{ResponseWriter: w}
	defer func() {
		logger.Info(
			"handled HTTP request",
			"status", responseWriter.statusCode(),
			"response_bytes", responseWriter.bytesWritten,
			"duration", time.Since(started),
		)
	}()

	if r.URL.Path == "/v1/models" {
		h.serveModels(responseWriter, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/models/") {
		h.serveModel(responseWriter, r, logger)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		http.NotFound(responseWriter, r)
		return
	}

	snapshot := h.snapshot.Load()
	if snapshot == nil {
		logger.Warn("rejecting inference request", "reason", "provider inventory is unavailable")
		writeError(responseWriter, http.StatusServiceUnavailable, "No inference providers are available.", "service_unavailable")
		return
	}
	h.proxy(responseWriter, r, snapshot, logger)
}

func (h *ModelsHandler) serveModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "Only GET is supported for /v1/models.", "invalid_request_error")
		return
	}

	snapshot, err := h.refresh(r.Context())
	if err != nil {
		h.logger.Error("refreshing provider models", "error", err)
		writeError(w, http.StatusServiceUnavailable, "No inference providers are available.", "service_unavailable")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(modelsResponse{Object: "list", Data: snapshot.models}); err != nil {
		h.logger.Error("writing models response", "error", err)
	}
}

func (h *ModelsHandler) serveModel(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "Only GET is supported for /v1/models/{model}.", "invalid_request_error")
		return
	}

	snapshot := h.snapshot.Load()
	if snapshot == nil {
		logger.Warn("rejecting model request", "reason", "provider inventory is unavailable")
		writeError(w, http.StatusServiceUnavailable, "No inference providers are available.", "service_unavailable")
		return
	}

	modelID := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	route, exists := snapshot.routes[modelID]
	if !exists {
		if _, ambiguous := snapshot.ambiguous[modelID]; ambiguous {
			logger.Warn("rejecting model request", "reason", "model is ambiguous", "model", modelID)
			writeError(w, http.StatusBadRequest, "Model ID is ambiguous; use a provider-qualified model ID.", "invalid_request_error")
			return
		}
		logger.Warn("rejecting model request", "reason", "model does not exist", "model", modelID)
		writeError(w, http.StatusNotFound, "The requested model does not exist.", "invalid_request_error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(route.model); err != nil {
		logger.Error("writing model response", "model", modelID, "error", err)
	}
}

func (h *ModelsHandler) Refresh(ctx context.Context) error {
	_, err := h.refresh(ctx)
	return err
}

func (h *ModelsHandler) refresh(ctx context.Context) (*routingSnapshot, error) {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()

	snapshot, err := h.refreshLocked(ctx)
	h.snapshot.Store(snapshot)
	return snapshot, err
}

func (h *ModelsHandler) refreshLocked(ctx context.Context) (*routingSnapshot, error) {
	started := time.Now()
	allProviders, err := h.listProviders(ctx)
	if err != nil {
		return nil, err
	}
	availableProviders := make([]providers.Provider, 0, len(allProviders))
	for _, provider := range allProviders {
		if provider.BaseURL != "" {
			availableProviders = append(availableProviders, provider)
		}
	}
	if len(availableProviders) == 0 {
		return nil, errors.New("no providers with an API base URL")
	}

	results := make(chan providerModels, len(availableProviders))
	var requests sync.WaitGroup
	for _, provider := range availableProviders {
		requests.Add(1)
		go func() {
			defer requests.Done()
			started := time.Now()
			models, err := h.fetchModels(ctx, provider.BaseURL)
			results <- providerModels{
				provider: provider,
				models:   models,
				err:      err,
				duration: time.Since(started),
			}
		}()
	}
	requests.Wait()
	close(results)

	snapshot := &routingSnapshot{
		models:    make([]modelOutput, 0),
		routes:    make(map[string]modelRoute),
		ambiguous: make(map[string]struct{}),
	}
	unqualifiedRoutes := make(map[string][]modelRoute)
	healthyProviders := 0
	for result := range results {
		if result.err != nil {
			h.logger.Error(
				"refreshing provider",
				"provider", result.provider.Name,
				"duration", result.duration,
				"error", result.err,
			)
			continue
		}
		h.logger.Info(
			"refreshed provider",
			"provider", result.provider.Name,
			"models", len(result.models),
			"duration", result.duration,
		)
		healthyProviders++
		seen := make(map[string]struct{}, len(result.models))
		for _, model := range result.models {
			if model.ID == "" {
				h.logger.Warn("ignoring model with empty ID", "provider", result.provider.Name)
				continue
			}
			publicID := result.provider.Name + "/" + model.ID
			if _, exists := seen[publicID]; exists {
				continue
			}
			seen[publicID] = struct{}{}
			output := modelOutput{
				ID:      publicID,
				Object:  "model",
				Created: model.Created,
				OwnedBy: result.provider.Name,
			}
			route := modelRoute{
				providerName:  result.provider.Name,
				baseURL:       result.provider.BaseURL,
				nativeModelID: model.ID,
				model:         output,
			}
			snapshot.routes[publicID] = route
			unqualifiedRoutes[model.ID] = append(unqualifiedRoutes[model.ID], route)
			snapshot.models = append(snapshot.models, output)
		}
	}
	if healthyProviders == 0 {
		return nil, errors.New("all providers failed")
	}

	for modelID, routes := range unqualifiedRoutes {
		if len(routes) == 1 {
			snapshot.routes[modelID] = routes[0]
			continue
		}
		snapshot.ambiguous[modelID] = struct{}{}
	}
	sort.Slice(snapshot.models, func(i, j int) bool { return snapshot.models[i].ID < snapshot.models[j].ID })
	h.logger.Info(
		"refreshed provider models",
		"providers_discovered", len(allProviders),
		"providers_available", len(availableProviders),
		"providers_healthy", healthyProviders,
		"models", len(snapshot.models),
		"ambiguous_models", len(snapshot.ambiguous),
		"duration", time.Since(started),
	)
	return snapshot, nil
}

func (h *ModelsHandler) fetchModels(ctx context.Context, baseURL string) ([]upstreamModel, error) {
	ctx, cancel := context.WithTimeout(ctx, modelDiscoveryTimeout)
	defer cancel()

	endpoint, err := modelsURL(baseURL)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := h.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("requesting models: %w", redactURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("requesting models: upstream returned %s", response.Status)
	}

	body := io.LimitReader(response.Body, maxModelsResponseSize+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading models: %w", err)
	}
	if len(data) > maxModelsResponseSize {
		return nil, fmt.Errorf("reading models: response exceeds %d bytes", maxModelsResponseSize)
	}

	var result upstreamModelsResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decoding models: %w", err)
	}
	if result.Data == nil {
		return nil, errors.New("decoding models: missing data array")
	}
	return result.Data, nil
}

func modelsURL(baseURL string) (string, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing provider URL: %w", err)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/models"
	endpoint.RawPath = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func (h *ModelsHandler) proxy(
	w http.ResponseWriter,
	r *http.Request,
	snapshot *routingSnapshot,
	logger *slog.Logger,
) {
	modelRequest, requestErr := parseModelRequest(r)
	if requestErr != nil {
		attributes := []any{"reason", requestErr.reason}
		if requestErr.err != nil {
			attributes = append(attributes, "error", requestErr.err)
		}
		logger.Warn("rejecting inference request", attributes...)
		writeError(w, requestErr.status, requestErr.message, "invalid_request_error")
		return
	}
	requestedModel := modelRequest.model

	route, exists := snapshot.routes[requestedModel]
	if !exists {
		if _, ambiguous := snapshot.ambiguous[requestedModel]; ambiguous {
			logger.Warn("rejecting inference request", "reason", "model is ambiguous", "model", requestedModel)
			writeError(w, http.StatusBadRequest, "Model ID is ambiguous; use a provider-qualified model ID.", "invalid_request_error")
			return
		}
		logger.Warn("rejecting inference request", "reason", "model does not exist", "model", requestedModel)
		writeError(w, http.StatusNotFound, "The requested model does not exist.", "invalid_request_error")
		return
	}
	logger.Info(
		"routing inference request",
		"provider", route.providerName,
		"model", requestedModel,
		"upstream_model", route.nativeModelID,
		"request_bytes", modelRequest.bodySize,
	)

	if err := modelRequest.rewrite(route.nativeModelID); err != nil {
		logger.Error("rewriting upstream request", "provider", route.providerName, "model", requestedModel, "error", err)
		writeError(w, http.StatusInternalServerError, "Unable to encode request body.", "internal_error")
		return
	}
	target, err := url.Parse(route.baseURL)
	if err != nil {
		logger.Error("parsing provider URL", "provider", route.providerName, "error", redactURLError(err))
		writeError(w, http.StatusBadGateway, "The inference provider is unavailable.", "service_unavailable")
		return
	}

	proxy := &httputil.ReverseProxy{}
	proxy.Rewrite = func(request *httputil.ProxyRequest) {
		request.Out.URL.Scheme = target.Scheme
		request.Out.URL.Host = target.Host
		request.Out.URL.User = target.User
		request.Out.URL.Path, request.Out.URL.RawPath = proxyPath(target, request.In.URL)
		request.Out.URL.RawQuery = joinQueries(target.RawQuery, request.In.URL.RawQuery)
		request.Out.Host = target.Host
		request.SetXForwarded()
		request.Out.Header.Del("Authorization")
	}
	proxy.Transport = h.client.Transport
	if proxy.Transport == nil {
		proxy.Transport = http.DefaultTransport
	}
	proxy.FlushInterval = -1
	proxy.ModifyResponse = func(response *http.Response) error {
		mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if strings.EqualFold(mediaType, "text/event-stream") {
			response.Header.Set("X-Accel-Buffering", "no")
		}
		return nil
	}
	proxy.ErrorHandler = func(responseWriter http.ResponseWriter, request *http.Request, proxyErr error) {
		handleProxyError(responseWriter, request, proxyErr, logger, route.providerName, requestedModel)
	}
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		if recovered != http.ErrAbortHandler {
			panic(recovered)
		}
		logger.Info(
			"upstream response stream terminated",
			"provider", route.providerName,
			"model", requestedModel,
		)
	}()
	proxy.ServeHTTP(w, r)
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status       int
	wroteHeader  bool
	bytesWritten int64
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(data)
	w.bytesWritten += int64(written)
	return written, err
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *loggingResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *loggingResponseWriter) WroteHeader() bool {
	return w.wroteHeader
}

func (w *loggingResponseWriter) MarkStatus(status int) {
	if !w.wroteHeader {
		w.status = status
	}
}

func redactURLError(err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		return fmt.Errorf("%s: %w", urlError.Op, urlError.Err)
	}
	return err
}

func proxyPath(baseURL, requestURL *url.URL) (string, string) {
	pathSuffix := strings.TrimPrefix(requestURL.Path, "/v1")
	path := strings.TrimRight(baseURL.Path, "/") + "/" + strings.TrimLeft(pathSuffix, "/")

	escapedSuffix := strings.TrimPrefix(requestURL.EscapedPath(), "/v1")
	rawPath := strings.TrimRight(baseURL.EscapedPath(), "/") + "/" + strings.TrimLeft(escapedSuffix, "/")
	if rawPath == (&url.URL{Path: path}).EscapedPath() {
		rawPath = ""
	}
	return path, rawPath
}

func joinQueries(baseQuery, requestQuery string) string {
	if baseQuery == "" {
		return requestQuery
	}
	if requestQuery == "" {
		return baseQuery
	}
	return baseQuery + "&" + requestQuery
}

func writeError(w http.ResponseWriter, status int, message, errorType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: errorOutput{Message: message, Type: errorType},
	})
}
