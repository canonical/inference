package status

import (
	"context"

	snapctlenv "github.com/canonical/go-snapctl/env"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

type Status struct {
	Services map[string]string
	BaseURL  string
	Health   map[string]string
	Warnings []string
}

type Collector struct {
	SnapdClient        *snapd.Client
	SnapCatalog        *snapcatalog.Reader
	ShareProvidersPath string
	BaseURL            string
}

func (c Collector) ListProviders(ctx context.Context) ([]providers.Provider, error) {
	return providers.ListInstalled(ctx, c.SnapCatalog, c.SnapdClient, c.ShareProvidersPath)
}

func (c Collector) Collect(ctx context.Context) (Status, error) {
	instanceName := snapctlenv.SnapInstanceName()
	if instanceName == "" {
		instanceName = "inference"
	}
	serviceStatus, err := c.SnapdClient.ServiceStatus(ctx, instanceName, "d")
	if err != nil {
		return Status{}, err
	}

	list, err := c.ListProviders(ctx)
	if err != nil {
		return Status{}, err
	}
	health := providers.CheckHealth(ctx, list)
	var healthOutput map[string]string
	if health != nil {
		healthOutput = make(map[string]string, len(health))
		for name, state := range health {
			healthOutput[name] = string(state)
		}
	}

	return Status{
		Services: map[string]string{instanceName + ".d": serviceStatus},
		BaseURL:  c.BaseURL,
		Health:   healthOutput,
	}, nil
}
