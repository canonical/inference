package common

import (
	"fmt"
	"strings"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/spf13/cobra"
)

func CompleteProviderNames(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func ValidateProvider(catalog *snapcatalog.Reader, name string) error {
	found, err := catalog.Contains(name)
	if err != nil {
		return fmt.Errorf("reading snap catalog: %w", err)
	}
	if !found {
		return fmt.Errorf("unknown inference provider %q", name)
	}
	return nil
}
