package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/status"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"
)

type statusCommand struct {
	*common.Context
	format string
}

type statusOutput struct {
	Services map[string]string            `json:"services" yaml:"services"`
	Proxy    map[string]map[string]string `json:"proxy,omitempty" yaml:"proxy,omitempty"`
	Health   map[string]string            `json:"health,omitempty" yaml:"health,omitempty"`
	Warnings []string                     `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

func Status(ctx *common.Context) *cobra.Command {
	cmd := statusCommand{Context: ctx}
	cobraCmd := &cobra.Command{
		Use:               "status",
		Short:             "Show inference status",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	cobraCmd.Flags().StringVar(&cmd.format, "format", "yaml", "output format [yaml|json]")
	return cobraCmd
}

func (cmd *statusCommand) run(cobraCmd *cobra.Command, _ []string) error {
	if cmd.format != "yaml" && cmd.format != "json" {
		return fmt.Errorf("unknown format %q", cmd.format)
	}

	status, err := cmd.buildStatus(cobraCmd.Context())
	if err != nil {
		return err
	}
	var output string
	if cmd.format == "json" {
		output, err = cmd.outputJSON(status)
	} else {
		output, err = cmd.outputYAML(status)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.Stdout, output)
	return err
}

func (cmd *statusCommand) buildStatus(ctx context.Context) (statusOutput, error) {
	statusResult, err := status.Lookup(ctx, status.Options{
		SnapCatalog:        cmd.SnapCatalog,
		SnapdClient:        cmd.SnapdClient,
		ShareProvidersPath: cmd.ShareProvidersPath,
		HTTPHost:           cmd.HTTPHost,
		HTTPPort:           cmd.HTTPPort,
	})
	if err != nil {
		return statusOutput{}, common.FriendlySnapdError(err)
	}

	return statusOutput{
		Services: statusResult.Services,
		Proxy:    statusResult.Proxy,
		Health:   statusResult.Health,
		Warnings: statusResult.Warnings,
	}, nil
}

func (cmd *statusCommand) outputJSON(status statusOutput) (string, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(status); err != nil {
		return "", err
	}
	return output.String(), nil
}

func (cmd *statusCommand) outputYAML(status statusOutput) (string, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(status); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return output.String(), nil
}
