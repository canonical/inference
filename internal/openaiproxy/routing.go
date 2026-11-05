package openaiproxy

import (
	"context"
	"time"

	"github.com/canonical/inference/internal/models"
)

type modelRoute struct {
	providerName  string
	baseURL       string
	nativeModelID string
	model         modelOutput
}

type routingSnapshot []modelRoute

func (h *ProxyHandler) RefreshRouting(ctx context.Context) error {
	_, err := h.refreshRouting(ctx)
	return err
}

func (h *ProxyHandler) refreshRouting(ctx context.Context) (*routingSnapshot, error) {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()

	snapshot, err := h.discoverRoutes(ctx)
	if err == nil {
		h.snapshot.Store(snapshot)
	}
	return snapshot, err
}

func (h *ProxyHandler) discoverRoutes(ctx context.Context) (*routingSnapshot, error) {
	started := time.Now()
	allProviders, err := h.listProviders(ctx)
	if err != nil {
		return nil, err
	}
	providerModels, err := models.Discover(ctx, allProviders, h.client)
	if err != nil {
		return nil, err
	}
	discoveredModels := make([]models.Model, 0)
	for _, provider := range providerModels {
		discoveredModels = append(discoveredModels, provider.Models...)
	}
	snapshot := make(routingSnapshot, 0, len(discoveredModels))
	for _, model := range discoveredModels {
		output := modelOutput{
			ID:      model.PublicID,
			Object:  "model",
			Created: model.Created,
			OwnedBy: model.ProviderName,
		}
		route := modelRoute{
			providerName:  model.ProviderName,
			baseURL:       model.ProviderBaseURL,
			nativeModelID: model.NativeID,
			model:         output,
		}
		snapshot = append(snapshot, route)
	}
	h.logger.Info(
		"refreshed provider models",
		"models", len(snapshot),
		"duration", time.Since(started),
	)
	return &snapshot, nil
}

func (s *routingSnapshot) route(modelID string) (modelRoute, bool) {
	for _, route := range *s {
		if route.model.ID == modelID {
			return route, true
		}
	}
	return modelRoute{}, false
}
