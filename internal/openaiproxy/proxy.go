package openaiproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/status"
)

const (
	maxProxyRequestSize = 16 * 1024 * 1024
	statusClientClosed  = 499
)

type ProxyHandler struct {
	listProviders func(context.Context) ([]providers.Provider, error)
	lookupStatus  func(context.Context) (status.Status, error)
	client        *http.Client
	logger        *slog.Logger
	refreshMu     sync.Mutex
	snapshot      atomic.Pointer[routingSnapshot]
	requestID     atomic.Uint64
}

type HandlerOptions struct {
	ListProviders func(context.Context) ([]providers.Provider, error)
	LookupStatus  func(context.Context) (status.Status, error)
	Client        *http.Client
	Logger        *slog.Logger
}

type errorResponse struct {
	Error errorOutput `json:"error"`
}

type errorOutput struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func NewProxyHandler(options HandlerOptions) *ProxyHandler {
	if options.Client == nil {
		options.Client = http.DefaultClient
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &ProxyHandler{
		listProviders: options.ListProviders,
		lookupStatus:  options.LookupStatus,
		client:        options.Client,
		logger:        options.Logger,
	}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := h.requestID.Add(1)
	logger := h.logger.With(
		"request_id", requestID,
		"method", r.Method,
		"path", r.URL.Path,
	)
	responseWriter := &loggingResponseWriter{ResponseWriter: w}
	setCORSHeaders(responseWriter.Header())
	defer func() {
		logger.Info(
			"handled HTTP request",
			"status", responseWriter.statusCode(),
			"response_bytes", responseWriter.bytesWritten,
			"duration", time.Since(started),
		)
	}()

	if isCORSPreflight(r) {
		responseWriter.WriteHeader(http.StatusNoContent)
		return
	}

	if r.URL.Path == "/" || r.URL.Path == "/v1" || r.URL.Path == "/v1/" {
		h.serveStatus(responseWriter, r, logger)
		return
	}
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
	h.proxyRequest(responseWriter, r, snapshot, logger)
}

func (h *ProxyHandler) proxyRequest(
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

	route, exists := snapshot.route(requestedModel)
	if !exists {
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
		logger.Error(
			"parsing provider URL",
			"provider", route.providerName,
			"provider_url", redactProviderURLForLogging(route.baseURL),
			"error", redactURLError(err),
		)
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
		removeCORSHeaders(response.Header)
		mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if strings.EqualFold(mediaType, "text/event-stream") {
			response.Header.Set("X-Accel-Buffering", "no")
		}
		return nil
	}
	proxy.ErrorHandler = func(responseWriter http.ResponseWriter, request *http.Request, proxyErr error) {
		handleProxyError(
			responseWriter,
			request,
			proxyErr,
			logger,
			route.providerName,
			route.baseURL,
			requestedModel,
		)
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
			"provider_url", redactProviderURLForLogging(route.baseURL),
			"model", requestedModel,
		)
	}()
	proxy.ServeHTTP(w, r)
}

func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		r.Header.Get("Origin") != "" &&
		r.Header.Get("Access-Control-Request-Method") != "" &&
		!isWebSocketUpgrade(r)
}

func setCORSHeaders(header http.Header) {
	removeCORSHeaders(header)
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Methods", "*")
	header.Set("Access-Control-Allow-Headers", "*, Authorization")
	header.Set("Access-Control-Expose-Headers", "*")
}

func removeCORSHeaders(header http.Header) {
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			header.Del(name)
		}
	}
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

func redactProviderURLForLogging(rawURL string) string {
	redacted, err := providers.RedactURL(rawURL)
	if err != nil {
		return "<invalid>"
	}
	return redacted
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
