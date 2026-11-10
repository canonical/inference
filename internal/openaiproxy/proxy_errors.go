package openaiproxy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

type responseStatusMarker interface {
	WroteHeader() bool
	MarkStatus(int)
}

func handleProxyError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	requestLogger *slog.Logger,
	providerName string,
	providerURL string,
	model string,
) {
	marker, _ := w.(responseStatusMarker)
	responseStarted := marker != nil && marker.WroteHeader()
	cause := context.Cause(r.Context())

	switch {
	case errors.Is(cause, http.ErrServerClosed):
		requestLogger.Info(
			"cancelling inference request during server shutdown",
			"provider", providerName,
			"model", model,
		)
		if !responseStarted {
			writeError(w, http.StatusServiceUnavailable, "The inference service is shutting down.", "service_unavailable")
		}
	case r.Context().Err() != nil:
		requestLogger.Debug(
			"client disconnected from inference request",
			"provider", providerName,
			"model", model,
		)
		if marker != nil && !responseStarted {
			marker.MarkStatus(statusClientClosed)
		}
	case errors.Is(err, context.Canceled):
		requestLogger.Info(
			"inference request was cancelled",
			"provider", providerName,
			"model", model,
		)
		if !responseStarted {
			writeError(w, http.StatusServiceUnavailable, "The inference request was cancelled.", "service_unavailable")
		}
	default:
		requestLogger.Error(
			"proxying inference request",
			"provider", providerName,
			"provider_url", redactProviderURLForLogging(providerURL),
			"model", model,
			"error", redactURLError(err),
		)
		if !responseStarted {
			writeError(w, http.StatusBadGateway, "The inference provider is unavailable.", "service_unavailable")
		}
	}
}
