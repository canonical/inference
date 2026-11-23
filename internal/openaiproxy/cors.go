package openaiproxy

import (
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

func (h *ModelsHandler) handleCORS(w http.ResponseWriter, r *http.Request, logger *slog.Logger) bool {
	removeCORSHeaders(w.Header())
	// Prevent caches from reusing responses across different origins.
	w.Header().Add("Vary", "Origin")

	origins := r.Header.Values("Origin")
	// Allow direct requests from non-browser clients such as curl.
	if len(origins) == 0 {
		return false
	}
	if len(origins) > 1 {
		logger.Warn("rejecting request", "reason", "multiple origins are not allowed")
		writeError(w, http.StatusForbidden, "Multiple request origins are not allowed.", "invalid_request_error")
		return true
	}
	allowedOrigin := origins[0]
	if h.allowAllOrigins {
		allowedOrigin = "*"
	} else if !isLoopbackOrigin(allowedOrigin) {
		logger.Warn("rejecting request", "reason", "origin is not loopback")
		writeError(w, http.StatusForbidden, "The request origin must be loopback.", "invalid_request_error")
		return true
	}

	if isCORSPreflight(r) {
		if !isAllowedCORSPreflight(r) {
			logger.Warn("rejecting request", "reason", "preflight method or headers are not allowed")
			writeError(w, http.StatusForbidden, "The requested CORS method or headers are not allowed.", "invalid_request_error")
			return true
		}
		setCORSHeaders(w.Header(), allowedOrigin)
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	setCORSHeaders(w.Header(), allowedOrigin)
	return false
}

func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		r.Header.Get("Origin") != "" &&
		r.Header.Get("Access-Control-Request-Method") != "" &&
		!isWebSocketUpgrade(r)
}

func isLoopbackOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return address.IsLoopback()
}

func isAllowedCORSPreflight(r *http.Request) bool {
	methods := r.Header.Values("Access-Control-Request-Method")
	if len(methods) != 1 || (methods[0] != http.MethodGet && methods[0] != http.MethodPost) {
		return false
	}
	for _, value := range r.Header.Values("Access-Control-Request-Headers") {
		for _, name := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "authorization", "content-type":
			default:
				return false
			}
		}
	}
	return true
}

func setCORSHeaders(header http.Header, origin string) {
	removeCORSHeaders(header)
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Access-Control-Allow-Methods", "GET, POST")
	header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
}

func removeCORSHeaders(header http.Header) {
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			header.Del(name)
		}
	}
}
