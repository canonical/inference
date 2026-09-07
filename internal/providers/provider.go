package providers

import (
	"context"
	"fmt"
	"sort"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

type ProviderType string

const (
	TypeInferenceSnap ProviderType = "inference-snap"
	TypeOpenAI        ProviderType = "openai"

	StateNotInstalled = "not installed"
	StateDisabled     = "disabled"
	StateEnabled      = "enabled"
)

type Provider struct {
	Name  string
	Type  ProviderType
	State string
}

func (p Provider) Installed() bool {
	return p.State != StateNotInstalled
}

func List(ctx context.Context, catalog *snapcatalog.Reader, snapdClient *snapd.Client, installedOnly bool) ([]Provider, error) {
	availableSnaps, err := catalog.Read()
	if err != nil {
		return nil, fmt.Errorf("reading snap catalog: %w", err)
	}

	result := make([]Provider, 0, len(availableSnaps))
	for _, entry := range availableSnaps {
		snapStatus, err := snapdClient.Status(ctx, entry.SnapName)
		if err != nil {
			return nil, fmt.Errorf("reading snap status for %q: %w", entry.SnapName, err)
		}
		state, err := snapStatusToProviderState(snapStatus)
		if err != nil {
			return nil, fmt.Errorf("reading snap status for %q: %w", entry.SnapName, err)
		}
		if installedOnly && state == StateNotInstalled {
			continue
		}
		result = append(result, Provider{Name: entry.SnapName, Type: TypeInferenceSnap, State: state})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func snapStatusToProviderState(snapStatus string) (string, error) {
	switch snapStatus {
	case snapd.StatusNotInstalled:
		return StateNotInstalled, nil
	case snapd.SnapStatusActive:
		return StateEnabled, nil
	case snapd.SnapStatusInstalled:
		return StateDisabled, nil
	default:
		return "", fmt.Errorf("unexpected snapd status %q", snapStatus)
	}
}
