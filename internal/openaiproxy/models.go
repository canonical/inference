package openaiproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/canonical/inference/internal/providers"
)

const maxModelsResponseSize = 4 << 20

type ModelsHandler struct {
	listProviders func(context.Context) ([]providers.Provider, error)
	client        *http.Client
	logger        *slog.Logger
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
	if r.URL.Path != "/v1/models" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "Only GET is supported for /v1/models.", "invalid_request_error")
		return
	}

	models, err := h.refresh(r.Context())
	if err != nil {
		h.logger.Error("refreshing provider models", "error", err)
		writeError(w, http.StatusServiceUnavailable, "No inference providers are available.", "service_unavailable")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(modelsResponse{Object: "list", Data: models}); err != nil {
		h.logger.Error("writing models response", "error", err)
	}
}

func (h *ModelsHandler) refresh(ctx context.Context) ([]modelOutput, error) {
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
			models, err := h.fetchModels(ctx, provider.BaseURL)
			results <- providerModels{provider: provider, models: models, err: err}
		}()
	}
	requests.Wait()
	close(results)

	models := make([]modelOutput, 0)
	healthyProviders := 0
	for result := range results {
		if result.err != nil {
			h.logger.Error("refreshing provider", "provider", result.provider.Name, "error", result.err)
			continue
		}
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
			models = append(models, modelOutput{
				ID:      publicID,
				Object:  "model",
				Created: model.Created,
				OwnedBy: result.provider.Name,
			})
		}
	}
	if healthyProviders == 0 {
		return nil, errors.New("all providers failed")
	}

	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (h *ModelsHandler) fetchModels(ctx context.Context, baseURL string) ([]upstreamModel, error) {
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
		return nil, fmt.Errorf("requesting models: %w", err)
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

func writeError(w http.ResponseWriter, status int, message, errorType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: errorOutput{Message: message, Type: errorType},
	})
}
