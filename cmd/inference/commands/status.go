package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"

	snapctlenv "github.com/canonical/go-snapctl/env"
	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"
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
		Short:             "Show status of services and system",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	cobraCmd.Flags().StringVar(&cmd.format, "format", "yaml", "output format [yaml|json]")
	cobraCmd.Flags().BoolVar(&cmd.Verbose, "verbose", false, "enable verbose output")
	return cobraCmd
}

func (cmd *statusCommand) run(cobraCmd *cobra.Command, _ []string) error {
	if cmd.format != "yaml" && cmd.format != "json" {
		return fmt.Errorf("unknown format %q", cmd.format)
	}

	status, providerProblems, err := cmd.buildStatus(cobraCmd.Context())
	if err != nil {
		return err
	}
	if cmd.Verbose {
		for _, problem := range providerProblems {
			if _, err := fmt.Fprintf(cmd.Stderr, "Error: %v\n", problem); err != nil {
				return err
			}
		}
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

func (cmd *statusCommand) buildStatus(ctx context.Context) (statusOutput, []error, error) {
	snapInstanceName := snapctlenv.SnapInstanceName()
	if snapInstanceName == "" {
		// Fall back to snap name when not inside a snap
		snapInstanceName = common.InferenceSnapName
	}
	serviceStatus, err := cmd.SnapdClient.ServiceStatus(ctx, snapInstanceName, common.InferenceService)
	if err != nil {
		return statusOutput{}, nil, common.FriendlySnapdError(err)
	}

	health, providerProblems, err := cmd.providerStatus(ctx)
	if err != nil {
		return statusOutput{}, nil, err
	}

	// TODO look up warnings like GPU not working

	return statusOutput{
		Services: map[string]string{snapInstanceName + "." + common.InferenceService: serviceStatus},
		Proxy: &statusProxy{
			OpenAI: statusOpenAIProxy{
				BaseURL: cmd.proxyOpenAIBaseURL(),
			},
		},
		Health: health,
	}, providerProblems, nil
}

func (cmd *statusCommand) providerStatus(ctx context.Context) (map[string]string, []error, error) {
	providerService := providers.NewService(cmd.SnapCatalog, cmd.SnapdClient, cmd.ShareProvidersPath)
	list, err := providerService.ListInstalled(ctx)
	if err != nil {
		return nil, nil, common.FriendlySnapdError(err)
	}
	return providerHealth(ctx, list), providerProblems(list), nil
}

func providerHealth(ctx context.Context, list []providers.Provider) map[string]string {
	health := providers.CheckHealth(ctx, list)
	if health == nil {
		return nil
	}
	output := make(map[string]string, len(health))
	for name, status := range health {
		output[name] = string(status)
	}
	return output
}

func providerProblems(list []providers.Provider) []error {
	var problems []error
	for _, provider := range list {
		if provider.Err != nil {
			problems = append(problems, fmt.Errorf("provider %q: %w", provider.Name, provider.Err))
		}
	}
	return problems
}

func (cmd *statusCommand) proxyOpenAIBaseURL() string {
	return "http://" + net.JoinHostPort(cmd.HTTPHost, cmd.HTTPPort) + "/v1"
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
