package status

import (
	"context"
	"net"

	snapctlenv "github.com/canonical/go-snapctl/env"
	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

type Options struct {
	SnapCatalog        *snapcatalog.Reader
	SnapdClient        *snapd.Client
	ShareProvidersPath string
	HTTPHost           string
	HTTPPort           string
}

type Status struct {
	Services map[string]string
	Proxy    map[string]map[string]string
	Health   map[string]string
	Warnings []string
}

func Lookup(ctx context.Context, options Options) (Status, error) {
	snapInstanceName := snapctlenv.SnapInstanceName()
	if snapInstanceName == "" {
		snapInstanceName = config.InferenceSnapName
	}
	serviceStatus, err := options.SnapdClient.ServiceStatus(ctx, snapInstanceName, config.InferenceService)
	if err != nil {
		return Status{}, err
	}

	health, err := providerHealth(ctx, options)
	if err != nil {
		return Status{}, err
	}

	return Status{
		Services: map[string]string{snapInstanceName + "." + config.InferenceService: serviceStatus},
		Proxy: map[string]map[string]string{
			"openai": {
				"base-url": "http://" + net.JoinHostPort(options.HTTPHost, options.HTTPPort) + "/v1",
			},
		},
		Health: health,
	}, nil
}

func providerHealth(ctx context.Context, options Options) (map[string]string, error) {
	list, err := providers.ListInstalled(
		ctx,
		options.SnapCatalog,
		options.SnapdClient,
		options.ShareProvidersPath,
	)
	if err != nil {
		return nil, err
	}
	health := providers.CheckHealth(ctx, list)
	if health == nil {
		return nil, nil
	}
	result := make(map[string]string, len(health))
	for name, healthStatus := range health {
		result[name] = string(healthStatus)
	}
	return result, nil
}
