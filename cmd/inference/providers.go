package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/canonical/inference/internal/providers"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
)

type providersCommand struct {
	*Context
	format    string
	installed bool
}

func Providers(ctx *Context) *cobra.Command {
	cmd := providersCommand{Context: ctx}
	cobraCmd := &cobra.Command{
		Use:               "providers",
		Short:             "List inference providers",
		Long:              "List available inference providers and whether they are installed.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	cobraCmd.Flags().StringVar(&cmd.format, "format", "table", "output format (table, json)")
	cobraCmd.Flags().BoolVar(&cmd.installed, "installed", false, "exclude providers that are not installed")
	return cobraCmd
}

func (cmd *providersCommand) run(cobraCmd *cobra.Command, _ []string) error {
	if cmd.format != "table" && cmd.format != "json" {
		return fmt.Errorf("unknown format %q", cmd.format)
	}

	list, warnings, err := cmd.Providers.List(cobraCmd.Context(), cmd.installed)
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		if _, err := fmt.Fprintf(cmd.Stderr, "Warning: %s\n", warning); err != nil {
			return err
		}
	}

	var output string
	if cmd.format == "json" {
		output, err = renderProvidersJSON(list)
	} else {
		output, err = renderProvidersTable(list)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.Stdout, output)
	return err
}

func renderProvidersJSON(list []providers.Provider) (string, error) {
	if list == nil {
		list = []providers.Provider{}
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(struct {
		Providers []providers.Provider `json:"providers"`
	}{list}); err != nil {
		return "", err
	}
	return output.String(), nil
}

func renderProvidersTable(list []providers.Provider) (string, error) {
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
	table.Header([]string{"PROVIDER", "TYPE", "STATUS"})
	showHint := false
	for _, p := range list {
		if err := table.Append([]string{p.Name, string(p.Type), p.Status}); err != nil {
			return "", err
		}
		if !p.Installed() {
			showHint = true
		}
	}
	if err := table.Render(); err != nil {
		return "", err
	}
	lines := strings.Split(output.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	result := strings.Join(lines, "\n")
	if showHint {
		result += "\n" + `Hint: run "inference install <provider>" to install providers.` + "\n"
	}
	return result, nil
}
