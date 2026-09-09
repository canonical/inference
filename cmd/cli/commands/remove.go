package commands

import (
	"github.com/canonical/inference/cmd/cli/common"
	"github.com/spf13/cobra"
)

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
		Long:              "Remove the inference snap with the provided name.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: common.CompleteSnapNames,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	return cobraCmd
}

func (cmd *removeCommand) run(cobraCmd *cobra.Command, args []string) error {
	return cmd.remove(cobraCmd, args[0])
}

func (cmd *removeCommand) remove(cobraCmd *cobra.Command, name string) error {
	// Validation of the provider is important to only allow snaps from the catalog to be removed
	if err := common.ValidateSnapName(cmd.ctx.SnapCatalog, name); err != nil {
		return err
	}
	return common.RemoveSnap(cobraCmd.Context(), cmd.ctx, name)
}
