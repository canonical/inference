package common

import (
	"fmt"
	"strings"

	snapctlenv "github.com/canonical/go-snapctl/env"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/spf13/cobra"
)

const (
	InferenceSnapName = "inference"
	InferenceService  = "d"
)

func IsSnap() bool {
	return snapctlenv.Snap() != "" && snapctlenv.SnapName() == InferenceSnapName
}

// CompleteSnapNames is used for tab completion. It only returns inference snap names from the catalog.
func CompleteSnapNames(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	entries, err := snapcatalog.NewReader().Read()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	matches := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.SnapName, toComplete) {
			matches = append(matches, entry.SnapName)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}

// ValidateSnapName checks if the given snap name is present in the catalog.
func ValidateSnapName(catalog *snapcatalog.Reader, name string) error {
	found, err := catalog.Contains(name)
	if err != nil {
		return fmt.Errorf("reading snap catalog: %w", err)
	}
	if !found {
		return fmt.Errorf("unknown inference snap %q", name)
	}
	return nil
}
