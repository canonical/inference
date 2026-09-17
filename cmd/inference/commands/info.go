package commands

import (
	"fmt"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"
)

type infoCommand struct {
	*common.Context
}

type infoOutput struct {
	Name  string     `yaml:"name"`
	Type  string     `yaml:"type"`
	State string     `yaml:"state"`
	API   *apiOutput `yaml:"api,omitempty"`
}

type apiOutput struct {
	OpenAI openAIOutput `yaml:"openai"`
}

type openAIOutput struct {
	BaseURL string `yaml:"base-url"`
}

func Info(ctx *common.Context) *cobra.Command {
	cmd := infoCommand{Context: ctx}
	cobraCmd := &cobra.Command{
		Use:               "info <provider>",
        Short:             "Show information about a provider",  
        Long:              "Show information about an inference provider, including its state and API details.",  
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: common.CompleteSnapNames,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	return cobraCmd
}

func (cmd *infoCommand) run(cobraCmd *cobra.Command, args []string) error {
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

	infoYaml, err := renderInfo(provider)
	if err != nil {
		return err
	}

	_, err = fmt.Fprint(cmd.Stdout, infoYaml)
	return err
}

func renderInfo(p providers.Provider) (string, error) {
	output := infoOutput{Name: p.Name, Type: string(p.Type), State: string(p.State)}
	if p.BaseURL != "" {
		output.API = &apiOutput{OpenAI: openAIOutput{BaseURL: providers.RedactedURL(p.BaseURL)}}
	}

	data, err := yaml.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
