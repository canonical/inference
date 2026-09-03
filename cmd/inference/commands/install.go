package commands

import (
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/spf13/cobra"
)

type installCommand struct {
	ctx         *common.Context
	snapdClient *snapd.Client
	catalog     *snapcatalog.Reader
}

func Install(ctx *common.Context) *cobra.Command {
	cmd := installCommand{
		ctx:         ctx,
		snapdClient: snapd.NewClient(),
		catalog:     snapcatalog.NewReader(),
	}
	cobraCmd := &cobra.Command{
		Use:               "install <provider>",
		Short:             "Install an inference provider",
		Long:              "Install the inference snap with the provided name.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProviderNames,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	return cobraCmd
}

func (cmd *installCommand) run(cobraCmd *cobra.Command, args []string) error {
	return cmd.install(cobraCmd, args[0])
}

func (cmd *installCommand) install(cobraCmd *cobra.Command, name string) error {
	if err := validateProvider(cmd.catalog, name); err != nil {
		return err
	}
	return common.InstallSnap(cobraCmd.Context(), cmd.ctx, cmd.snapdClient, name)
}

func completeProviderNames(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	entries, err := snapcatalog.NewReader().Read()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	matches := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.SnapName, toComplete) {
			matches = append(matches, entry.SnapName)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}
