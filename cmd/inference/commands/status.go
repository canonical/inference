package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/snapd"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

const (
	inferenceSnapName = "inference"
	proxyServiceName  = "d"
)

type statusCommand struct {
	*common.Context
	format string
}

type statusOutput struct {
	Services map[string]string `json:"services" yaml:"services"`
	Proxy    *statusProxy      `json:"proxy,omitempty" yaml:"proxy,omitempty"`
	Health   map[string]string `json:"health,omitempty" yaml:"health,omitempty"`
	Warnings []string          `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

type statusProxy struct {
	OpenAI statusOpenAIProxy `json:"openai" yaml:"openai"`
}

type statusOpenAIProxy struct {
	BaseURL string `json:"base-url" yaml:"base-url"`
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

	status, err := buildStatus(cobraCmd.Context(), cmd.SnapdClient)
	if err != nil {
		return err
	}
	output, err := renderStatus(status, cmd.format)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.Stdout, output)
	return err
}

func buildStatus(ctx context.Context, snapdClient *snapd.Client) (statusOutput, error) {
	proxyStatus, err := snapdClient.ServiceStatus(ctx, inferenceSnapName, proxyServiceName)
	if err != nil {
		return statusOutput{}, common.FriendlySnapdError(err)
	}
	return statusOutput{
		Services: map[string]string{"proxy": proxyStatus},
	}, nil
}

func renderStatus(status statusOutput, format string) (string, error) {
	var output bytes.Buffer
	if format == "json" {
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(status); err != nil {
			return "", err
		}
		return output.String(), nil
	}

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
