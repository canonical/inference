package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
)

type infoCommand struct {
	*common.Context
	format string
}

type infoOutput struct {
	Name    string         `json:"name"`
	Backend string         `json:"backend"`
	State   string         `json:"state"`
	API     *apiOutput     `json:"api,omitempty"`
	Engines []engineOutput `json:"engines,omitempty"`
}

type apiOutput struct {
	OpenAI openAIOutput `json:"openai"`
}

type openAIOutput struct {
	BaseURL string `json:"base-url"`
}

type engineOutput struct {
	Name         string   `json:"name"`
	Runtime      string   `json:"runtime"`
	DefaultModel string   `json:"default-model,omitempty"`
	Models       []string `json:"models"`
}

func Info(ctx *common.Context) *cobra.Command {
	cmd := infoCommand{Context: ctx}
	cobraCmd := &cobra.Command{
		Use:               "info <provider>",
		Short:             "Show information about a provider",
		Long:              "Show information about an inference provider, including its state, API details, and available engines with their models.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: common.CompleteSnapNames,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	cobraCmd.Flags().StringVar(&cmd.format, "format", "table", "output format [table|json]")
	return cobraCmd
}

func (cmd *infoCommand) run(cobraCmd *cobra.Command, args []string) error {
	if cmd.format != "table" && cmd.format != "json" {
		return fmt.Errorf("unknown format %q", cmd.format)
	}

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

	info, err := cmd.renderInfo(provider, cmd.format)
	if err != nil {
		return err
	}

	_, err = fmt.Fprint(cmd.Stdout, info)
	return err
}

func (cmd *infoCommand) renderInfo(p providers.Provider, format string) (string, error) {
	output := infoOutput{Name: p.Name, Backend: string(p.Backend), State: string(p.State)}
	if p.BaseURL != "" {
		redactedURL, err := providers.RedactURL(p.BaseURL)
		if err != nil {
			return "", fmt.Errorf("redacting provider base URL: %w", err)
		}
		output.API = &apiOutput{OpenAI: openAIOutput{BaseURL: redactedURL}}
	}

	for _, engine := range p.Engines {
		output.Engines = append(output.Engines, engineOutput{
			Name:         engine.Name,
			Runtime:      engine.Runtime,
			DefaultModel: engine.DefaultModel,
			Models:       engine.Models,
		})
	}
	if format == "json" {
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(output); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	return cmd.outputTable(p, output)
}

func (cmd *infoCommand) outputTable(p providers.Provider, output infoOutput) (string, error) {
	var buf bytes.Buffer
	stateMark := "✗"
	if p.State == providers.StateEnabled {
		stateMark = "✓"
	}
	fmt.Fprintf(&buf, "%s [%s] - %s %s\n", p.Name, p.Backend, p.State, stateMark)

	if p.State == providers.StateEnabled && output.API != nil {
		fmt.Fprintf(&buf, "\nAPI: %s (proxy)\n", cmd.proxyOpenAIBaseURL())
		fmt.Fprintf(&buf, "     └── %s (upstream)\n", output.API.OpenAI.BaseURL)
	}

	if p.Backend != providers.BackendSnap || len(p.Engines) == 0 {
		return buf.String(), nil
	}

	models := make([]string, 0)
	seenModels := make(map[string]struct{})
	for _, engine := range p.Engines {
		for _, model := range engine.Models {
			if _, ok := seenModels[model]; ok {
				continue
			}
			seenModels[model] = struct{}{}
			models = append(models, model)
		}
	}
	models = shortestModelNames(models)

	headers := []string{"ENGINE"}
	headers = append(headers, models...)
	rows := make([][]string, 0, len(p.Engines))
	for _, engine := range p.Engines {
		available := make(map[string]struct{}, len(engine.Models))
		for _, model := range engine.Models {
			for _, columnModel := range models {
				if model == columnModel || isSupersetModel(model, columnModel) {
					available[columnModel] = struct{}{}
				}
			}
		}
		row := make([]string, len(headers))
		row[0] = engine.Name
		for index, model := range models {
			if _, ok := available[model]; ok {
				row[index+1] = "✓"
			}
		}
		rows = append(rows, row)
	}

	buf.WriteByte('\n')
	tableMaxWidth := 120
	engineWidth := len(headers[0])
	for _, engine := range p.Engines {
		engineWidth = max(engineWidth, len(engine.Name))
	}
	engineWidth += 2
	engineWidth = min(engineWidth, max(3, tableMaxWidth-3*len(models)))
	widths := make(tw.Mapper[int, int], len(headers))
	widths[0] = engineWidth
	for index, model := range models {
		widths[index+1] = len(model) + 2
	}
	totalWidth := 0
	for _, width := range widths {
		totalWidth += width
	}
	if totalWidth > tableMaxWidth && len(models) > 0 {
		modelWidth := max(1, (tableMaxWidth-engineWidth)/len(models))
		for index := range models {
			widths[index+1] = modelWidth
		}
	}
	columnPadding := make([]tw.Padding, len(headers))
	columnPadding[0] = tw.Padding{Overwrite: true, Right: " "}
	for index := 1; index < len(columnPadding); index++ {
		columnPadding[index] = tw.Padding{Overwrite: true, Left: " "}
	}
	padding := tw.CellPadding{PerColumn: columnPadding}
	table := tablewriter.NewTable(
		&buf,
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
			MaxWidth: tableMaxWidth,
			Widths:   tw.CellWidth{PerColumn: widths},
			Header: tw.CellConfig{
				Formatting: tw.CellFormatting{AutoFormat: tw.Off},
				Alignment:  tw.CellAlignment{Global: tw.AlignLeft},
				Padding:    padding,
			},
			Row: tw.CellConfig{
				Formatting: tw.CellFormatting{AutoWrap: tw.WrapTruncate},
				Alignment:  tw.CellAlignment{Global: tw.AlignLeft},
				Padding:    padding,
			},
		}),
	)
	table.Header(headers)
	if err := table.Bulk(rows); err != nil {
		return "", err
	}
	if err := table.Render(); err != nil {
		return "", err
	}
	lines := strings.Split(buf.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n"), nil
}

func shortestModelNames(models []string) []string {
	shortest := make([]string, 0, len(models))
	for _, model := range models {
		isSuperset := false
		for _, other := range models {
			if isSupersetModel(model, other) {
				isSuperset = true
				break
			}
		}
		if !isSuperset {
			shortest = append(shortest, model)
		}
	}
	return shortest
}

func isSupersetModel(model, other string) bool {
	return len(model) > len(other) && strings.Contains(model, other)
}

func (cmd *infoCommand) proxyOpenAIBaseURL() string {
	host := cmd.HTTPHost
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, cmd.HTTPPort) + "/v1"
}
