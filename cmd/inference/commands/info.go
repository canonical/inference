package commands

import (
	"fmt"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/redact"
	"github.com/spf13/cobra"
)

type infoCommand struct {
	*common.Context
}

func Info(ctx *common.Context) *cobra.Command {
	cmd := infoCommand{Context: ctx}
	cobraCmd := &cobra.Command{
		Use:               "info <provider>",
		Short:             "Display details of a provider",
		Long:              "Display details of an installed or installable inference provider.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: common.CompleteSnapNames,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	return cobraCmd
}

func (cmd *infoCommand) run(cobraCmd *cobra.Command, args []string) error {
	provider, err := providers.Find(
		cobraCmd.Context(),
		cmd.SnapCatalog,
		cmd.SnapdClient,
		cmd.ShareProvidersPath,
		args[0],
	)
	if err != nil {
		return common.FriendlySnapdError(err)
	}

	_, err = fmt.Fprint(cmd.Stdout, renderInfo(provider))
	return err
}

func renderInfo(p providers.Provider) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", p.Name)
	fmt.Fprintf(&b, "type: %s\n", string(p.Type))
	fmt.Fprintf(&b, "state: %s\n", string(p.State))
	if p.BaseURL != "" {
		b.WriteString("api\n")
		b.WriteString("  openai\n")
		fmt.Fprintf(&b, "    base-url: %s\n", redact.URL(p.BaseURL))
	}
	return b.String()
}
