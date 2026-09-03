package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/canonical/inference/cmd/inference/commands"
	"github.com/canonical/inference/cmd/inference/common"
	"github.com/spf13/cobra"
)

func main() {
	ctx := &common.Context{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	commandCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Stop intercepting once the first interrupt arrives so a second one
	// force-quits instead of being swallowed by the abort window.
	go func() {
		<-commandCtx.Done()
		stop()
	}()

	if err := root(ctx).ExecuteContext(commandCtx); err != nil {
		os.Exit(1)
	}
}

func root(ctx *common.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "inference",
		Short:             "Manage inference snaps",
		Long:              "inference provides a CLI to discover, install and remove inference snaps.",
		SilenceUsage:      true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	cmd.CompletionOptions.HiddenDefaultCmd = true
	cmd.SetOut(ctx.Stdout)
	cmd.SetErr(ctx.Stderr)
	cmd.AddCommand(
		commands.Providers(ctx),
		commands.Install(ctx),
		commands.Remove(ctx),
	)
	return cmd
}
