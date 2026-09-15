package providers

import (
	"context"
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
	checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	port := endpoint.Port()
	if port == "" {
		if endpoint.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	connection, err := (&net.Dialer{}).DialContext(checkCtx, "tcp", net.JoinHostPort(endpoint.Hostname(), port))
	if err != nil {
		return HealthOffline
	}
	if err := connection.Close(); err != nil {
		return HealthError
	}

	healthURL := *endpoint
	healthURL.Path = "/health"
	healthURL.RawPath = ""
	healthURL.RawQuery = ""
	healthURL.Fragment = ""
	status, err := requestHealth(checkCtx, healthURL.String())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return HealthNotResponsive
		}
		return HealthError
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return HealthOK
	}
	if status != http.StatusNotFound {
		return HealthError
	}

	modelsURL := *endpoint
	modelsURL.Path = strings.TrimRight(modelsURL.Path, "/") + "/models"
	modelsURL.RawPath = ""
	modelsURL.RawQuery = ""
	modelsURL.Fragment = ""
	status, err = requestHealth(checkCtx, modelsURL.String())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return HealthNotResponsive
		}
		return HealthError
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return HealthOK
	}
	return HealthError
}

func requestHealth(ctx context.Context, endpoint string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}
