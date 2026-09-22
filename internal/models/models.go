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
	PublicID        string
	NativeID        string
	Created         int64
	ProviderName    string
	ProviderBaseURL string
}

type upstreamModel struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
}

func List(
	ctx context.Context,
	catalog *snapcatalog.Reader,
	snapdClient *snapd.Client,
	shareProvidersPath string,
) ([]Model, error) {
	allProviders, err := providers.ListAll(
		ctx,
		catalog,
		snapdClient,
		shareProvidersPath,
	)
	if err != nil {
		return nil, err
	}
	return Discover(ctx, allProviders, http.DefaultClient)
}

func Discover(
	ctx context.Context,
	allProviders []providers.Provider,
	client *http.Client,
) ([]Model, error) {
	availableProviders := make([]providers.Provider, 0, len(allProviders))
	providerNames := make(map[string]struct{}, len(allProviders))
	for _, provider := range allProviders {
		if provider.BaseURL == "" {
			continue
		}
		if provider.Type == providers.TypeInferenceSnap && provider.State != providers.StateEnabled {
			continue
		}
		if provider.Name == "" {
			return nil, errors.New("provider with an API base URL has an empty name")
		}
		if _, exists := providerNames[provider.Name]; exists {
			return nil, fmt.Errorf("duplicate provider name %q", provider.Name)
		}
		providerNames[provider.Name] = struct{}{}
		availableProviders = append(availableProviders, provider)
	}

	if len(availableProviders) == 0 {
		return []Model{}, nil
	}
	if client == nil {
		client = http.DefaultClient
	}

	type providerModels struct {
		models []Model
		err    error
	}
	results := make(chan providerModels, len(availableProviders))
	var requests sync.WaitGroup
	for _, provider := range availableProviders {
		requests.Go(func() {
			upstreamModels, err := fetchModels(ctx, client, provider.BaseURL)
			results <- providerModels{
				models: normalizeModels(provider, upstreamModels),
				err:    err,
			}
		})
	}
	requests.Wait()
	close(results)

	models := make([]Model, 0)
	healthyProviders := 0
	for result := range results {
		if result.err == nil {
			healthyProviders++
			models = append(models, result.models...)
		}
	}
	if healthyProviders == 0 {
		return nil, errors.New("all providers failed")
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].PublicID < models[j].PublicID
	})
	return models, nil
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
