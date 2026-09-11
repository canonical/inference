package providers

import (
	"fmt"

	"github.com/canonical/inference/internal/snapcatalog"
)

func CatalogSnapProviders(catalog *snapcatalog.Reader) ([]Provider, error) {
	if catalog == nil {
		return nil, fmt.Errorf("reading snap catalog: catalog reader is not configured")
	}
	entries, err := catalog.Read()
	if err != nil {
		return nil, fmt.Errorf("reading snap catalog: %w", err)
	}

	result := make([]Provider, len(entries))
	for i, entry := range entries {
		result[i] = Provider{
			Name:       entry.SnapName,
			Type:       TypeInferenceSnap,
			State:      StateUnknown,
			Connection: ConnectionNotConnected,
		}
	}
	return result, nil
}
