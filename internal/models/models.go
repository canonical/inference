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
)

const (
	maxResponseSize = 4 * 1024 * 1024
	requestTimeout  = 15 * time.Second
)

type Model struct {
	PublicID        string
	NativeID        string
	Created         int64
	ProviderName    string
	ProviderBaseURL string
}

// ProviderModels holds the outcome of querying one provider; Err is set when its query failed.
type ProviderModels struct {
	ProviderName string
	Models       []Model
	Err          error
}

type upstreamModel struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
}

type Service interface {
	List(ctx context.Context) ([]ProviderModels, error)
}

type service struct {
	providers  providers.Service
	httpClient *http.Client
}

func NewService(providerService providers.Service, httpClient *http.Client) Service {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &service{providers: providerService, httpClient: httpClient}
}

func (s *service) List(ctx context.Context) ([]ProviderModels, error) {
	allProviders, err := s.providers.List(ctx)
	if err != nil {
		return nil, err
	}

	availableProviders := make([]providers.Provider, 0, len(allProviders))
	providerNames := make(map[string]struct{}, len(allProviders))
	for _, provider := range allProviders {
		if provider.Err == nil && !isQueryable(provider) {
			continue
		}
		if provider.Name == "" {
			return nil, errors.New("provider has an empty name")
		}
		if _, exists := providerNames[provider.Name]; exists {
			return nil, fmt.Errorf("duplicate provider name %q", provider.Name)
		}
		providerNames[provider.Name] = struct{}{}
		availableProviders = append(availableProviders, provider)
	}

	if len(availableProviders) == 0 {
		return []ProviderModels{}, nil
	}

	results := make(chan ProviderModels, len(availableProviders))
	var requests sync.WaitGroup
	for _, provider := range availableProviders {
		if provider.Err != nil {
			results <- ProviderModels{ProviderName: provider.Name, Models: []Model{}, Err: provider.Err}
			continue
		}
		requests.Go(func() {
			result := ProviderModels{ProviderName: provider.Name, Models: []Model{}}
			upstreamModels, err := fetchModels(ctx, s.httpClient, provider.BaseURL)
			result.Err = err
			if err == nil {
				result.Models = normalizeModels(provider, upstreamModels)
				sort.Slice(result.Models, func(i, j int) bool {
					return result.Models[i].NativeID < result.Models[j].NativeID
				})
			}

			results <- result
		})
	}
	requests.Wait()
	close(results)

	pm := make([]ProviderModels, 0, len(availableProviders))
	for result := range results {
		pm = append(pm, result)
	}
	sort.Slice(pm, func(i, j int) bool {
		return pm[i].ProviderName < pm[j].ProviderName
	})
	return pm, nil
}

func isQueryable(provider providers.Provider) bool {
	if provider.Backend == providers.BackendSnap && provider.State != providers.StateEnabled {
		return false
	}
	return provider.BaseURL != ""
}

func normalizeModels(provider providers.Provider, upstreamModels []upstreamModel) []Model {
	output := make([]Model, 0, len(upstreamModels))
	seen := make(map[string]struct{}, len(upstreamModels))
	for _, model := range upstreamModels {
		if model.ID == "" {
			continue
		}
		if _, exists := seen[model.ID]; exists {
			continue
		}
		seen[model.ID] = struct{}{}
		output = append(output, Model{
			PublicID:        provider.Name + "/" + model.ID,
			NativeID:        model.ID,
			Created:         model.Created,
			ProviderName:    provider.Name,
			ProviderBaseURL: provider.BaseURL,
		})
	}
	return output
}

func fetchModels(ctx context.Context, client *http.Client, baseURL string) ([]upstreamModel, error) {
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

	var result struct {
		Data []upstreamModel `json:"data"`
	}
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
		return "", fmt.Errorf("parsing provider URL: %w", redactURLError(err))
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
