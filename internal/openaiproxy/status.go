package openaiproxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type statusResponse struct {
	Services map[string]string            `json:"services"`
	Proxy    map[string]map[string]string `json:"proxy,omitempty"`
	Health   map[string]string            `json:"health,omitempty"`
	Warnings []string                     `json:"warnings,omitempty"`
}

func (h *ProxyHandler) serveStatus(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "Only GET is supported for "+r.URL.Path+".", "invalid_request_error")
		return
	}
	if h.lookupStatus == nil {
		logger.Error("status lookup is not configured")
		writeError(w, http.StatusServiceUnavailable, "Inference status is unavailable.", "service_unavailable")
		return
	}
	result, err := h.lookupStatus(r.Context())
	if err != nil {
		logger.Error("looking up inference status", "error", err)
		writeError(w, http.StatusServiceUnavailable, "Inference status is unavailable.", "service_unavailable")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(statusResponse{
		Services: result.Services,
		Proxy:    result.Proxy,
		Health:   result.Health,
		Warnings: result.Warnings,
	}); err != nil {
		logger.Error("writing status response", "error", err)
	}
}
