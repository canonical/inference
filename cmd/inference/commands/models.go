package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/canonical/inference/cmd/inference/common"
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
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
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

	list, err := fetchDaemonModels(cobraCmd.Context(), cmd.HTTPHost, cmd.HTTPPort)
	if err != nil {
		return err
	}
	return cmd.printModels(list)
}

func fetchDaemonModels(ctx context.Context, host, port string) (_ []modelOutput, err error) {
	// request models from the daemon
	endpoint := "http://" + net.JoinHostPort(host, port) + "/v1/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("requesting models from daemon: %w", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing daemon models response: %w", closeErr))
		}
	}()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("requesting models from daemon: returned %s", response.Status)
	}
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding daemon models: %w", err)
	}
	if result.Data == nil {
		return nil, fmt.Errorf("decoding daemon models: missing data array")
	}
	list := make([]modelOutput, len(result.Data))
	for i, model := range result.Data {
		nativeID, ok := strings.CutPrefix(model.ID, model.OwnedBy+"/")
		if model.OwnedBy == "" || !ok || nativeID == "" {
			return nil, fmt.Errorf("decoding daemon models: invalid model ID %q for provider %q", model.ID, model.OwnedBy)
		}
		list[i] = modelOutput{ID: model.ID, Provider: model.OwnedBy, Model: nativeID}
	}

	return list, nil
}

func (cmd *modelsCommand) printModels(models []modelOutput) error {
	var output string
	var err error

	if cmd.format == "json" {
		output, err = cmd.outputJSON(models)
	} else {
		output, err = cmd.outputTable(models)
	}
	if err != nil {
		return err
	}

	_, err = fmt.Fprint(cmd.Stdout, output)

	return err
}

func (cmd *modelsCommand) outputJSON(list []modelOutput) (string, error) {
	result := modelsOutput{Models: make([]modelOutput, len(list))}
	copy(result.Models, list)

	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return "", err
	}
	return output.String(), nil
}

func (cmd *modelsCommand) outputTable(list []modelOutput) (string, error) {
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
	table.Header([]string{"PROVIDER", "MODEL"})
	previousProvider := ""
	for _, model := range list {
		provider := model.Provider
		if provider == previousProvider {
			provider = ""
		} else {
			previousProvider = provider
		}
		if err := table.Append([]string{provider, model.Model}); err != nil {
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
