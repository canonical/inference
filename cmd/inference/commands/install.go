package commands

import (
	"fmt"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/spf13/cobra"
)

type installCommand struct {
	ctx *common.Context
}

func Install(ctx *common.Context) *cobra.Command {
	cmd := installCommand{
		ctx: ctx,
	}
	cobraCmd := &cobra.Command{
		Use:               "install <provider>",
		Short:             "Install a provider",
		Long:              "Install the inference snap with the provided name.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: common.CompleteSnapNames,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	return cobraCmd
}

func (cmd *installCommand) run(cobraCmd *cobra.Command, args []string) error {
	return cmd.install(cobraCmd, args[0])
}

func (cmd *installCommand) install(cobraCmd *cobra.Command, name string) error {
	// Validation of the provider is important to only allow snaps from the catalog to be installed
	if err := common.ValidateSnapName(cmd.ctx.SnapCatalog, name); err != nil {
		return err
	}
	if err := common.InstallSnap(cobraCmd.Context(), cmd.ctx, name); err != nil {
		return err
	}
	if _, err := cmd.ctx.Config.EnsureProviders([]string{name}); err != nil {
		return fmt.Errorf("adding %s to config: %w", name, err)
	}
	return nil
}
