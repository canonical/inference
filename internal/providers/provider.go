package providers

import (
	"context"
	"fmt"
	"sort"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

type Type string

const TypeInferenceSnap Type = "inference-snap"
const StatusNotInstalled = "not installed"

type Provider struct {
	Name   string `json:"provider"`
	Type   Type   `json:"type"`
	Status string `json:"status"`
}

func (p Provider) Installed() bool {
	return p.Status != StatusNotInstalled
}

func List(ctx context.Context, installedOnly bool) ([]Provider, error) {
	availableSnaps, err := snapcatalog.NewReader().Read()
	if err != nil {
		return nil, fmt.Errorf("reading snap catalog: %w", err)
	}

	snapStatuses, err := snapd.NewClient().Statuses(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading snap statuses: %w", err)
	}

	result := make([]Provider, 0, len(availableSnaps))
	for _, entry := range availableSnaps {
		status, installed := snapStatuses[entry.SnapName]
		if !installed {
			status = StatusNotInstalled
		}
		if installedOnly && !installed {
			continue
		}
		result = append(result, Provider{Name: entry.SnapName, Type: TypeInferenceSnap, Status: status})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
