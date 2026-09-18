package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/canonical/inference/cmd/inference/commands"
	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/spf13/cobra"
)

const (
	sharedProvidersPathEnvVar = "SHARED_PROVIDERS_PATH"
	httpHostEnvVar            = "HTTP_HOST"
	httpPortEnvVar            = "HTTP_PORT"
	defaultHTTPHost           = "127.0.0.1"
	defaultHTTPPort           = "8400"
)

func main() {
	ctx := &common.Context{
		Stdout:             os.Stdout,
		Stderr:             os.Stderr,
		SnapdClient:        snapd.NewClient(),
		SnapCatalog:        snapcatalog.NewReader(),
		ShareProvidersPath: shareProvidersPath(),
		HTTPHost:           httpHost(),
		HTTPPort:           httpPort(),
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

func shareProvidersPath() string {
	if path := os.Getenv(sharedProvidersPathEnvVar); path != "" {
		return path
	}
	if snapRoot := os.Getenv("SNAP"); snapRoot != "" {
		return filepath.Join(snapRoot, "share/providers")
	}
	return ""
}

func httpHost() string {
	host := os.Getenv(httpHostEnvVar)
	if host == "" || host == "0.0.0.0" {
		return defaultHTTPHost
	}
	return host
}

func httpPort() string {
	if port := os.Getenv(httpPortEnvVar); port != "" {
		return port
	}
	return defaultHTTPPort
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
		commands.Models(ctx),
		commands.Status(ctx),
	)
	return cmd
}
