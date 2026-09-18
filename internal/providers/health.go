package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const healthCheckTimeout = 5 * time.Second

type HealthStatus string

const (
	HealthOK            HealthStatus = "ok"
	HealthOffline       HealthStatus = "offline"
	HealthNotResponsive HealthStatus = "not responsive"
	HealthError         HealthStatus = "error"
)

func CheckHealth(ctx context.Context, providers []Provider) map[string]HealthStatus {
	type result struct {
		name   string
		health HealthStatus
	}

	// Only check enabled providers that have a base URL configured.
	enabled := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider.State == StateEnabled && provider.BaseURL != "" {
			enabled = append(enabled, provider)
		}
	}
	if len(enabled) == 0 {
		return nil
	}

	results := make(chan result, len(enabled))
	var checks sync.WaitGroup
	checks.Add(len(enabled))
	// Check providers in parallel so one unresponsive provider does not delay the others.
	for _, provider := range enabled {
		go func() {
			defer checks.Done()
			results <- result{
				name:   provider.Name,
				health: checkHealth(ctx, provider.BaseURL),
			}
		}()
	}
	checks.Wait()
	close(results)

	health := make(map[string]HealthStatus, len(enabled))
	for result := range results {
		health[result.name] = result.health
	}
	return health
}

func checkHealth(ctx context.Context, baseURL string) HealthStatus {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return HealthError
	}
	modelsURL := *endpoint
	modelsURL.Path = strings.TrimRight(modelsURL.Path, "/") + "/models"
	modelsURL.RawPath = ""
	modelsURL.RawQuery = ""
	modelsURL.Fragment = ""

	checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	status, models, err := requestModels(checkCtx, modelsURL.String())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return HealthNotResponsive
		}
		var operationError *net.OpError
		if errors.As(err, &operationError) {
			if operationError.Timeout() {
				return HealthNotResponsive
			}
			return HealthOffline
		}
		return HealthError
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices &&
		(len(models.Data) > 0 || len(models.Models) > 0) {
		return HealthOK
	}
	return HealthError
}

type modelsResponse struct {
	Data   []json.RawMessage `json:"data"`
	Models []json.RawMessage `json:"models"`
}

func requestModels(ctx context.Context, endpoint string) (int, modelsResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, modelsResponse{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, modelsResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return response.StatusCode, modelsResponse{}, nil
	}

	var models modelsResponse
	if err := json.NewDecoder(response.Body).Decode(&models); err != nil {
		return response.StatusCode, modelsResponse{}, err
	}
	return response.StatusCode, models, nil
}
