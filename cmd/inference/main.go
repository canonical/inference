package main

import (
	"io"
	"os"

	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/spf13/cobra"
)

type Context struct {
	Stdout    io.Writer
	Stderr    io.Writer
	Providers *providers.Service
}

func main() {
	ctx := &Context{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Providers: providers.NewService(
			snapcatalog.NewResolver(),
			map[providers.Type]providers.StatusSource{
				providers.TypeInferenceSnap: snapd.NewClient(),
			},
		),
	}

	if err := root(ctx).Execute(); err != nil {
		os.Exit(1)
	}
}

func root(ctx *Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "inference",
		Short:             "Manage inference providers",
		Long:              "inference discovers and manages inference-snap providers for local inference.",
		SilenceUsage:      true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	cmd.CompletionOptions.HiddenDefaultCmd = true
	cmd.SetOut(ctx.Stdout)
	cmd.SetErr(ctx.Stderr)
	cmd.AddCommand(Providers(ctx), SnapCatalogSeed())
	return cmd
}
