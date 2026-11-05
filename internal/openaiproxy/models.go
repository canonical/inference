package openaiproxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

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

func (h *ProxyHandler) serveModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "Only GET is supported for /v1/models.", "invalid_request_error")
		return
	}

	snapshot, err := h.refreshRouting(r.Context())
	if err != nil {
		h.logger.Error("refreshing provider models", "error", err)
		writeError(w, http.StatusServiceUnavailable, "No inference providers are available.", "service_unavailable")
		return
	}

	models := make([]modelOutput, len(*snapshot))
	for i, route := range *snapshot {
		models[i] = route.model
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(modelsResponse{Object: "list", Data: models}); err != nil {
		h.logger.Error("writing models response", "error", err)
	}
}

func (h *ProxyHandler) serveModel(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
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
	route, exists := snapshot.route(modelID)
	if !exists {
		logger.Warn("rejecting model request", "reason", "model does not exist", "model", modelID)
		writeError(w, http.StatusNotFound, "The requested model does not exist.", "invalid_request_error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(route.model); err != nil {
		logger.Error("writing model response", "model", modelID, "error", err)
	}
}
