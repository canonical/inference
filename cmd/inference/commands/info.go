package commands

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"
)

type infoCommand struct {
	*common.Context
	format string
}

type infoOutput struct {
	Name    string        `json:"name" yaml:"name"`
	Type    string        `json:"type" yaml:"type"`
	State   string        `json:"state" yaml:"state"`
	API     *apiOutput    `json:"api,omitempty" yaml:"api,omitempty"`
	Engines enginesOutput `json:"engines,omitempty" yaml:"engines,omitempty"`
}

type apiOutput struct {
	OpenAI openAIOutput `json:"openai" yaml:"openai"`
}

type openAIOutput struct {
	BaseURL string `json:"base-url" yaml:"base-url"`
}

type engineOutput struct {
	Name         string   `json:"name"`
	Runtime      string   `json:"runtime"`
	DefaultModel string   `json:"default-model,omitempty"`
	Models       []string `json:"models"`
}

type enginesOutput []engineOutput

// MarshalYAML keys engines by name and marks the default model. JSON keeps the plain array.
func (e enginesOutput) MarshalYAML() (any, error) {
	type engineYAML struct {
		Runtime string   `yaml:"runtime"`
		Models  []string `yaml:"models"`
	}

	engines := make(map[string]engineYAML, len(e))
	for _, engine := range e {
		models := make([]string, len(engine.Models))
		for i, model := range engine.Models {
			models[i] = model
			if model == engine.DefaultModel {
				models[i] += " (default)"
			}
		}
		engines[engine.Name] = engineYAML{Runtime: engine.Runtime, Models: models}
	}
	return engines, nil
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
	cobraCmd.Flags().StringVar(&cmd.format, "format", "yaml", "output format [yaml|json]")
	return cobraCmd
}

func (cmd *infoCommand) run(cobraCmd *cobra.Command, args []string) error {
	if cmd.format != "yaml" && cmd.format != "json" {
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
	output := infoOutput{Name: p.Name, Type: string(p.Type), State: string(p.State)}
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

	var buf bytes.Buffer
	if format == "json" {
		encoder := json.NewEncoder(&buf)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(output); err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(output); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
