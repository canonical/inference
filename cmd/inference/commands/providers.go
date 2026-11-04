package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
)

type providersCommand struct {
	*common.Context
	format string
}

type providerJSON struct {
	Name    string `json:"provider"`
	Backend string `json:"backend"`
	Enabled bool   `json:"enabled"`
}

type providersJSONOutput struct {
	Providers []providerJSON `json:"providers"`
}

func Providers(ctx *common.Context) *cobra.Command {
	cmd := providersCommand{Context: ctx}
	cobraCmd := &cobra.Command{
		Use:               "providers",
		Short:             "List inference providers",
		Long:              "List inference providers and whether they are enabled.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	cobraCmd.Flags().StringVar(&cmd.format, "format", "table", "output format [table|json]")
	return cobraCmd
}

func (cmd *providersCommand) run(cobraCmd *cobra.Command, _ []string) error {
	if cmd.format != "table" && cmd.format != "json" {
		return fmt.Errorf("unknown format %q", cmd.format)
	}

	list, err := providers.ListInstalled(
		cobraCmd.Context(),
		cmd.Config,
		cmd.SnapCatalog,
		cmd.SnapdClient,
		cmd.ShareProvidersPath,
	)
	if err != nil {
		return common.FriendlySnapdError(err)
	}

	var output string
	if cmd.format == "json" {
		output, err = cmd.outputJSON(list)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(cmd.Stdout, output)
	} else {
		output, err = cmd.outputTable(list)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(cmd.Stdout, output)
		_, _ = fmt.Fprintln(cmd.Stderr, "\nHint: Run \"inference find\" to discover other inference snaps.")
	}

	return err
}

func (cmd *providersCommand) outputJSON(list []providers.Provider) (string, error) {
	if list == nil {
		list = []providers.Provider{}
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	result := providersJSONOutput{Providers: make([]providerJSON, len(list))}
	for i, p := range list {
		result.Providers[i] = providerJSON{
			Name:    p.Name,
			Backend: string(p.Backend),
			Enabled: p.State == providers.StateEnabled,
		}
	}
	if err := encoder.Encode(result); err != nil {
		return "", err
	}
	return output.String(), nil
}

func (cmd *providersCommand) outputTable(list []providers.Provider) (string, error) {
	var output bytes.Buffer
	table := tablewriter.NewTable(
		&output,
		tablewriter.WithRenderer(renderer.NewColorized(renderer.ColorizedConfig{
			Header:  renderer.Tint{FG: renderer.Colors{color.Bold}},
			Column:  renderer.Tint{FG: renderer.Colors{color.Reset}, BG: renderer.Colors{color.Reset}},
			Borders: tw.BorderNone,
			Settings: tw.Settings{
				Separators:  tw.Separators{ShowHeader: tw.Off, ShowFooter: tw.Off, BetweenRows: tw.Off, BetweenColumns: tw.Off},
				Lines:       tw.Lines{ShowTop: tw.Off, ShowBottom: tw.Off, ShowHeaderLine: tw.Off, ShowFooterLine: tw.Off},
				CompactMode: tw.On,
			},
		})),
		tablewriter.WithConfig(tablewriter.Config{
			MaxWidth: 80,
			Header: tw.CellConfig{
				Alignment: tw.CellAlignment{Global: tw.AlignLeft},
				Padding: tw.CellPadding{PerColumn: []tw.Padding{
					{Overwrite: true, Right: " "},
					{Overwrite: true, Left: " ", Right: " "},
					{Overwrite: true, Left: " "},
				}},
			},
			Row: tw.CellConfig{
				Formatting: tw.CellFormatting{AutoWrap: tw.WrapTruncate},
				Alignment:  tw.CellAlignment{Global: tw.AlignLeft},
				Padding: tw.CellPadding{PerColumn: []tw.Padding{
					{Overwrite: true, Right: " "},
					{Overwrite: true, Left: " ", Right: " "},
					{Overwrite: true, Left: " "},
				}},
			},
		}),
	)
	table.Header([]string{"PROVIDER", "BACKEND", "ENABLED"})
	for _, p := range list {
		enabled := "no"
		if p.State == providers.StateEnabled {
			enabled = "yes"
		}
		if err := table.Append([]string{p.Name, string(p.Backend), enabled}); err != nil {
			return "", err
		}
	}
	if err := table.Render(); err != nil {
		return "", err
	}
	lines := strings.Split(output.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n"), nil
}
