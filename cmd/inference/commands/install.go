package commands

import (
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
		Short:             "Install an inference provider",
		Long:              "Install the inference snap with the provided name.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: common.CompleteProviderNames,
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
	if err := common.ValidateProvider(cmd.ctx.SnapCatalog, name); err != nil {
		return err
	}
	return common.InstallSnap(cobraCmd.Context(), cmd.ctx, name)
}
