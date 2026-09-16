package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/models"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
)

type modelsCommand struct {
	*common.Context
	format string
}

type modelOutput struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type modelsOutput struct {
	Models []modelOutput `json:"models"`
}

func Models(ctx *common.Context) *cobra.Command {
	cmd := modelsCommand{Context: ctx}
	cobraCmd := &cobra.Command{
		Use:               "models",
		Short:             "List available models",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	cobraCmd.Flags().StringVar(&cmd.format, "format", "table", "output format [table|json]")
	return cobraCmd
}

func (cmd *modelsCommand) run(cobraCmd *cobra.Command, _ []string) error {
	if cmd.format != "table" && cmd.format != "json" {
		return fmt.Errorf("unknown format %q", cmd.format)
	}

	list, err := models.List(
		cobraCmd.Context(),
		cmd.SnapCatalog,
		cmd.SnapdClient,
		cmd.ShareProvidersPath,
	)
	if err != nil {
		return err
	}
	output, err := renderModels(list, cmd.format)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.Stdout, output)
	return err
}

func renderModels(list []models.Model, format string) (string, error) {
	if format == "table" {
		return renderModelsTable(list)
	}

	result := modelsOutput{Models: make([]modelOutput, len(list))}
	for i, model := range list {
		result.Models[i] = modelOutput{
			Name:     model.Name,
			Provider: model.Provider,
		}
	}

	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return "", err
	}
	return output.String(), nil
}

func renderModelsTable(list []models.Model) (string, error) {
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
					{Overwrite: true, Left: " "},
				}},
			},
			Row: tw.CellConfig{
				Formatting: tw.CellFormatting{AutoWrap: tw.WrapTruncate},
				Alignment:  tw.CellAlignment{Global: tw.AlignLeft},
				Padding: tw.CellPadding{PerColumn: []tw.Padding{
					{Overwrite: true, Right: " "},
					{Overwrite: true, Left: " "},
				}},
			},
		}),
	)
	table.Header([]string{"NAME", "PROVIDER"})
	for _, model := range list {
		if err := table.Append([]string{model.Name, model.Provider}); err != nil {
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
