package common

import (
	"slices"
	"strings"

	"github.com/canonical/inference/internal/config"
	"github.com/spf13/cobra"
)

// CompleteProviderName is used for tab completion. It returns both inference snap names from the catalog and configured provider names.
func CompleteProviderName(cfg *config.ConfigReader) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		matches, directive := CompleteSnapNames(cmd, args, toComplete)
		if directive&cobra.ShellCompDirectiveError != 0 {
			return nil, directive
		}

		configuredProviders, err := cfg.GetProviders()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		for name := range configuredProviders {
			if strings.HasPrefix(name, toComplete) {
				matches = append(matches, name)
			}
		}
		slices.Sort(matches)
		return slices.Compact(matches), directive
	}
}
