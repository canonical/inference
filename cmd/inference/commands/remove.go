package commands

import (
	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/spf13/cobra"
)

type removeCommand struct {
	ctx         *common.Context
	snapdClient *snapd.Client
	catalog     *snapcatalog.Reader
}

func Remove(ctx *common.Context) *cobra.Command {
	cmd := removeCommand{
		ctx:         ctx,
		snapdClient: snapd.NewClient(),
		catalog:     snapcatalog.NewReader(),
	}
	cobraCmd := &cobra.Command{
		Use:               "remove <provider>",
		Short:             "Remove an inference provider",
		Long:              "Remove the inference snap with the provided name.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProviderNames,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	return cobraCmd
}

func (cmd *removeCommand) run(cobraCmd *cobra.Command, args []string) error {
	return cmd.remove(cobraCmd, args[0])
}

func (cmd *removeCommand) remove(cobraCmd *cobra.Command, name string) error {
	if err := validateProvider(cmd.catalog, name); err != nil {
		return err
	}
	return common.RemoveSnap(cobraCmd.Context(), cmd.ctx, cmd.snapdClient, name)
}
