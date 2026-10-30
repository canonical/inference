package commands

import (
	"errors"
	"fmt"
	"os"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/config"
	"github.com/spf13/cobra"
)

var removeGeteuid = os.Geteuid

type removeCommand struct {
	ctx *common.Context
}

func Remove(ctx *common.Context) *cobra.Command {
	cmd := removeCommand{
		ctx: ctx,
	}
	cobraCmd := &cobra.Command{
		Use:               "remove <provider>",
		Short:             "Remove an inference provider",
		Long:              "Remove a custom provider or an inference snap with the provided name.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: common.CompleteProviderName(ctx.Config),
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	return cobraCmd
}

func (cmd *removeCommand) run(cobraCmd *cobra.Command, args []string) error {
	return cmd.remove(cobraCmd, args[0])
}

func (cmd *removeCommand) remove(cobraCmd *cobra.Command, name string) error {
	if removeGeteuid() != 0 {
		return errors.New("removing a provider requires root privileges. Try again using sudo")
	}

	providers, err := cmd.ctx.Config.GetProviders()
	if err != nil {
		return fmt.Errorf("reading configured providers: %w", err)
	}

	provider, existsInConfig := providers[name]
	if existsInConfig {
		return cmd.removeConfiguredProvider(provider, name)
	}

	// Validation of the provider is important to only allow snaps from the catalog to be removed
	if err := common.ValidateSnapName(cmd.ctx.SnapCatalog, name); err != nil {
		return err
	}
	return common.RemoveSnap(cobraCmd.Context(), cmd.ctx, name)
}

func (cmd *removeCommand) removeConfiguredProvider(provider config.Provider, name string) error {
	if provider.Backend != config.BackendProxy {
		return errors.New("removing non-proxy providers from config is not supported")
	}

	err := cmd.ctx.Config.RemoveProvider(name)
	if err != nil {
		return fmt.Errorf("removing provider: %w", err)
	}

	return nil
}
