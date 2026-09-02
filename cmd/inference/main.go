package main

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

type Context struct {
	Stdout io.Writer
	Stderr io.Writer
}

func main() {
	ctx := &Context{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	if err := root(ctx).Execute(); err != nil {
		os.Exit(1)
	}
}

func root(ctx *Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "inference",
		Short:             "Manage inference snaps",
		Long:              "inference provides a CLI to discover, install and manage inference snaps.",
		SilenceUsage:      true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	cmd.CompletionOptions.HiddenDefaultCmd = true
	cmd.SetOut(ctx.Stdout)
	cmd.SetErr(ctx.Stderr)
	cmd.AddCommand(
		Providers(ctx),
	)
	return cmd
}
