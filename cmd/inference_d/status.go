package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/canonical/inference/internal/status"
)

type statusOutput struct {
	Services map[string]string `json:"services"`
	Proxy    statusProxy       `json:"proxy"`
	Health   map[string]string `json:"health,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
}

type statusProxy struct {
	OpenAI statusOpenAIProxy `json:"openai"`
}

type statusOpenAIProxy struct {
	BaseURL string `json:"base-url"`
}

type statusErrorOutput struct {
	Error statusError `json:"error"`
}

type statusError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func newStatusHandler(collect func(context.Context) (status.Status, error), logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := collect(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			logger.Error("collecting inference status", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(statusErrorOutput{
				Error: statusError{Message: "Inference status is unavailable.", Type: "service_unavailable"},
			}); err != nil {
				logger.Error("writing status error response", "error", err)
			}
			return
		}

		output := statusOutput{
			Services: result.Services,
			Proxy:    statusProxy{OpenAI: statusOpenAIProxy{BaseURL: result.BaseURL}},
			Health:   result.Health,
			Warnings: result.Warnings,
		}
		if err := json.NewEncoder(w).Encode(output); err != nil {
			logger.Error("writing status response", "error", err)
		}
	})
}
