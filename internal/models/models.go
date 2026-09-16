package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

const (
	maxResponseSize = 4 * 1024 * 1024
	requestTimeout  = 15 * time.Second
)

type Model struct {
	Name     string
	Provider string
}

type upstreamResponse struct {
	Data []ProviderModel `json:"data"`
}

type ProviderModel struct {
	ID      string `json:"id"`
	Created int64  `json:"created,omitempty"`
}

type ProviderModels struct {
	Provider providers.Provider
	Models   []ProviderModel
	Err      error
	Duration time.Duration
}

func List(
	ctx context.Context,
	catalog *snapcatalog.Reader,
	snapdClient *snapd.Client,
	shareProvidersPath string,
) ([]Model, error) {
	list, err := providers.List(
		ctx,
		catalog,
		snapdClient,
		shareProvidersPath,
		providers.ListOptions{},
	)
	if err != nil {
		return nil, err
	}
	return listProviderModels(ctx, list, http.DefaultClient)
}

func listProviderModels(ctx context.Context, list []providers.Provider, client *http.Client) ([]Model, error) {
	available := make([]providers.Provider, 0, len(list))
	for _, provider := range list {
		if provider.BaseURL == "" {
			continue
		}
		if provider.Type == providers.TypeInferenceSnap && provider.State != providers.StateEnabled {
			continue
		}
		available = append(available, provider)
	}
	if len(available) == 0 {
		return []Model{}, nil
	}

	output := make([]Model, 0)
	healthyProviders := 0
	for _, result := range DiscoverProviderModels(ctx, available, client) {
		if result.Err != nil {
			continue
		}
		healthyProviders++
		seen := make(map[string]struct{}, len(result.Models))
		for _, model := range result.Models {
			if model.ID == "" {
				continue
			}
			if _, exists := seen[model.ID]; exists {
				continue
			}
			seen[model.ID] = struct{}{}
			output = append(output, Model{
				Name:     model.ID,
				Provider: providerLabel(result.Provider),
			})
		}
	}
	if healthyProviders == 0 {
		return nil, errors.New("all providers failed")
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].Provider == output[j].Provider {
			return output[i].Name < output[j].Name
		}
		return output[i].Provider < output[j].Provider
	})
	return output, nil
}

func DiscoverProviderModels(ctx context.Context, list []providers.Provider, client *http.Client) []ProviderModels {
	if client == nil {
		client = http.DefaultClient
	}

	results := make(chan ProviderModels, len(list))
	var requests sync.WaitGroup
	for _, provider := range list {
		requests.Add(1)
		go func() {
			defer requests.Done()
			started := time.Now()
			models, err := fetchModels(ctx, client, provider.BaseURL)
			results <- ProviderModels{
				Provider: provider,
				Models:   models,
				Err:      err,
				Duration: time.Since(started),
			}
		}()
	}
	requests.Wait()
	close(results)

	output := make([]ProviderModels, 0, len(list))
	for result := range results {
		output = append(output, result)
	}
	return output
}

func fetchModels(ctx context.Context, client *http.Client, baseURL string) ([]ProviderModel, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
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

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("requesting models: %w", redactURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("requesting models: upstream returned %s", response.Status)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading models: %w", err)
	}
	if len(data) > maxResponseSize {
		return nil, fmt.Errorf("reading models: response exceeds %d bytes", maxResponseSize)
	}

	var result upstreamResponse
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

func redactURLError(err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		return fmt.Errorf("%s: %w", urlError.Op, urlError.Err)
	}
	return err
}

func providerLabel(provider providers.Provider) string {
	if provider.Type == providers.TypeInferenceSnap {
		return provider.Name + " snap"
	}
	return provider.Name + " (remote)"
}
