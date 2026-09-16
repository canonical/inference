package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
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

	status, err := buildStatus(cobraCmd.Context(), cmd.Context)
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

func buildStatus(ctx context.Context, commandContext *common.Context) (statusOutput, error) {
	proxyStatus, err := commandContext.SnapdClient.ServiceStatus(ctx, inferenceSnapInstanceName(), proxyServiceName)
	if err != nil {
		return statusOutput{}, common.FriendlySnapdError(err)
	}

	baseURL, err := proxyOpenAIBaseURL(ctx)
	if err != nil {
		return statusOutput{}, err
	}

	health, err := statusProviderHealth(ctx, commandContext)
	if err != nil {
		return statusOutput{}, err
	}

	return statusOutput{
		Services: map[string]string{"proxy": proxyStatus},
		Proxy: &statusProxy{
			OpenAI: statusOpenAIProxy{
				BaseURL: baseURL,
			},
		},
		Health: health,
	}, nil
}

func inferenceSnapInstanceName() string {
	if name := os.Getenv("SNAP_INSTANCE_NAME"); name != "" {
		return name
	}
	return inferenceSnapName
}

func statusProviderHealth(ctx context.Context, commandContext *common.Context) (map[string]string, error) {
	list, err := providers.List(
		ctx,
		commandContext.SnapCatalog,
		commandContext.SnapdClient,
		commandContext.ShareProvidersPath,
		providers.ListOptions{},
	)
	if err != nil {
		return nil, common.FriendlySnapdError(err)
	}

	health := providers.CheckHealth(ctx, list)
	if health == nil {
		return nil, nil
	}
	output := make(map[string]string, len(health))
	for name, status := range health {
		output[name] = string(status)
	}
	return output, nil
}

func proxyOpenAIBaseURL(ctx context.Context) (string, error) {
	if os.Getenv("SNAP") == "" || os.Getenv("SNAP_NAME") != inferenceSnapName {
		return "unavailable", nil
	}

	host, err := snapConfigurationValue(ctx, "http.host")
	if err != nil {
		return "", err
	}
	if host == "" {
		return "", fmt.Errorf("inference snap configuration option http.host is empty")
	}

	port, err := snapConfigurationValue(ctx, "http.port")
	if err != nil {
		return "", err
	}
	if port == "" {
		return "", fmt.Errorf("inference snap configuration option http.port is empty")
	}
	return "http://" + net.JoinHostPort(host, port) + "/v1", nil
}

func snapConfigurationValue(ctx context.Context, key string) (string, error) {
	output, err := exec.CommandContext(ctx, "snapctl", "get", key).CombinedOutput()
	if err != nil {
		if message := strings.TrimSpace(string(output)); message != "" {
			return "", errors.New(message)
		}
		return "", fmt.Errorf("reading inference snap configuration option %s: %w", key, err)
	}
	return strings.TrimSpace(string(output)), nil
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
