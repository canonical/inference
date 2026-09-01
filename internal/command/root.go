package command

import (
	"github.com/spf13/cobra"
)

// Root builds the `inference` root command and attaches its subcommands.
func Root(ctx *Context) *cobra.Command {
	root := &cobra.Command{
		Use:               "inference",
		Short:             "Manage inference providers",
		Long:              "inference discovers and manages inference-snap providers for local inference.",
		SilenceUsage:      true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
	}

	root.CompletionOptions.HiddenDefaultCmd = true

	return root
}
