package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/spf13/cobra"
)

var errStorageAccessDenied = errors.New("permission denied. Try again using sudo")

type setEnabledCommand struct {
	*common.Context
	enabled bool
}

func newSetEnabledCommand(ctx *common.Context, enabled bool, use, short, long string) *cobra.Command {
	cmd := setEnabledCommand{Context: ctx, enabled: enabled}
	return &cobra.Command{
		Use:               use + " <provider>",
		Short:             short,
		Long:              long,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: common.CompleteSnapNames,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
}

func Enable(ctx *common.Context) *cobra.Command {
	return newSetEnabledCommand(ctx, true, "enable", "Enable a provider",
		"Enable an installed provider so its models are visible and usage is allowed.")
}

func Disable(ctx *common.Context) *cobra.Command {
	return newSetEnabledCommand(ctx, false, "disable", "Disable a provider",
		"Disable an installed provider so its models are hidden and usage is not allowed.")
}

func (cmd *setEnabledCommand) run(cobraCmd *cobra.Command, args []string) error {
	name := args[0]
	ctx := cobraCmd.Context()

	providerService := providers.NewService(cmd.SnapCatalog, cmd.SnapdClient, cmd.ShareProvidersPath, cmd.Storage)
	provider, err := providerService.Find(ctx, name)
	if err != nil {
		return common.FriendlySnapdError(err)
	}
	if !provider.Installed() {
		return fmt.Errorf("provider %q is not installed", name)
	}

	changed, err := cmd.Storage.SetProviderEnabled(name, cmd.enabled)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return errStorageAccessDenied
		}
		return fmt.Errorf("updating storage: %w", err)
	}

	verb := "Enabled"
	if !cmd.enabled {
		verb = "Disabled"
	}
	if changed {
		_, _ = fmt.Fprintf(cmd.Stdout, "%s %s\n", verb, name)
	} else {
		_, _ = fmt.Fprintf(cmd.Stdout, "%s is already %s\n", name, strings.ToLower(verb))
	}

	if err := common.TriggerModelDiscovery(ctx, cmd.Context); err != nil && !errors.Is(err, common.ErrServiceInactive) {
		_, _ = fmt.Fprintf(cmd.Stderr, "warning: triggering model discovery failed: %v\n", err)
	}
	return nil
}
