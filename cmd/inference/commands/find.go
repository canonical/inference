package commands

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
)

type findCommand struct {
	*common.Context
}

func Find(ctx *common.Context) *cobra.Command {
	cmd := findCommand{Context: ctx}
	return &cobra.Command{
		Use:               "find [query]...",
		Short:             "Search for inference snaps",
		Long:              "List inference snaps in the catalog or search for a particular one",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
}

func (cmd *findCommand) run(cobraCmd *cobra.Command, args []string) error {
	query := strings.TrimSpace(strings.Join(args, " "))
	entries, err := cmd.SnapCatalog.Read()
	if err != nil {
		return err
	}
	results := entries
	if query != "" {
		snaps, err := cmd.SnapdClient.Find(cobraCmd.Context(), query)
		if err != nil {
			return common.FriendlySnapdError(err)
		}
		catalog := make(map[string]snapcatalog.Entry, len(entries))
		for _, entry := range entries {
			catalog[entry.SnapName] = entry
		}
		rankFindResults(snaps, query)
		results = nil
		for _, snap := range snaps {
			if entry, ok := catalog[snap.Name]; ok {
				results = append(results, entry)
				delete(catalog, snap.Name)
			}
		}
	}
	if len(results) == 0 {
		_, err := fmt.Fprintf(cmd.Stderr, "No inference snaps matched the query %q\n", query)
		return err
	}
	output, err := cmd.outputTable(results)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.Stdout, output)
	return err
}

func rankFindResults(results []snapd.FindResult, query string) {
	words := strings.Fields(strings.ToLower(query))
	slices.Sort(words)
	words = slices.Compact(words)
	slices.SortStableFunc(results, func(first, second snapd.FindResult) int {
		firstMatches := findMatchCounts(first, words)
		secondMatches := findMatchCounts(second, words)
		for index := range firstMatches {
			if firstMatches[index] != secondMatches[index] {
				return secondMatches[index] - firstMatches[index]
			}
		}
		return 0
	})
}

func findMatchCounts(result snapd.FindResult, words []string) [4]int {
	var counts [4]int
	for index, field := range []string{result.Name, result.Title, result.Summary, result.Description} {
		field = strings.ToLower(field)
		for _, word := range words {
			if strings.Contains(field, word) {
				counts[index]++
			}
		}
	}
	return counts
}

func (cmd *findCommand) outputTable(entries []snapcatalog.Entry) (string, error) {
	headers := []string{"NAME", "CAPABILITIES", "OPTIMIZATIONS"}
	nameMaxLen := len(headers[0])
	optimizationsMaxLen := len(headers[2])
	capabilitiesMaxLen := len(headers[1])
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		optimizations := make([]string, 0, len(entry.Engines))
		for _, engine := range entry.Engines {
			optimizations = append(optimizations, engine.Name)
		}
		var capabilities []string
		for _, model := range entry.Models {
			for _, capability := range model.Capabilities {
				if capability != "" && !slices.Contains(capabilities, capability) {
					capabilities = append(capabilities, capability)
				}
			}
		}
		slices.Sort(capabilities)
		optimizationText := strings.Join(optimizations, ", ")
		if optimizationText == "" {
			optimizationText = "-"
		}
		capabilityText := strings.Join(capabilities, ", ")
		if capabilityText == "" {
			capabilityText = "-"
		}
		nameMaxLen = max(nameMaxLen, len(entry.SnapName))
		optimizationsMaxLen = max(optimizationsMaxLen, len(optimizationText))
		capabilitiesMaxLen = max(capabilitiesMaxLen, len(capabilityText))
		rows = append(rows, []string{entry.SnapName, capabilityText, optimizationText})
	}
	tableMaxWidth := 80
	optimizationsMinWidth := len(headers[2]) + 1
	capabilitiesMinWidth := len(headers[1]) + 2
	nameWidth := min(nameMaxLen+1, tableMaxWidth-optimizationsMinWidth-capabilitiesMinWidth)
	capabilitiesWidth := min(capabilitiesMaxLen+2, tableMaxWidth-nameWidth-optimizationsMinWidth)
	optimizationsWidth := min(optimizationsMaxLen+1, tableMaxWidth-nameWidth-capabilitiesWidth)
	var output bytes.Buffer
	padding := tw.CellPadding{PerColumn: []tw.Padding{
		{Overwrite: true, Right: " "},
		{Overwrite: true, Left: " ", Right: " "},
		{Overwrite: true, Left: " "},
	}}
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
			MaxWidth: tableMaxWidth,
			Widths: tw.CellWidth{PerColumn: tw.Mapper[int, int]{
				0: nameWidth,
				1: capabilitiesWidth,
				2: optimizationsWidth,
			}},
			Header: tw.CellConfig{Alignment: tw.CellAlignment{Global: tw.AlignLeft}, Padding: padding},
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
	lines := strings.Split(output.String(), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.Join(lines, "\n"), nil
}
