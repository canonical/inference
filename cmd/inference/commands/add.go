package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/spf13/cobra"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	providerOpenAI       = "openai"
)

var providerTypes = []string{providerOpenAI}

var addGeteuid = os.Geteuid

type addCommand struct {
	ctx  *common.Context
	name string
}

func Add(ctx *common.Context) *cobra.Command {
	cmd := addCommand{ctx: ctx}
	cobraCmd := &cobra.Command{
		Use:               "add <provider>\nAvailable providers: openai",
		Short:             "Add an inference provider",
		Long:              "Add a third-party inference provider with an API key. Set the OPENAI_API_KEY environment variable or enter it when prompted.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeAddProvider,
		SilenceUsage:      true,
		RunE:              cmd.run,
	}
	cobraCmd.Flags().StringVar(&cmd.name, "name", "", "provider name")
	_ = cobraCmd.RegisterFlagCompletionFunc("name", cobra.NoFileCompletions)
	return cobraCmd
}

func completeAddProvider(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var matches []string
	for _, provider := range providerTypes {
		if strings.HasPrefix(provider, toComplete) {
			matches = append(matches, provider)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}

func (cmd *addCommand) run(cobraCmd *cobra.Command, args []string) error {
	provider := args[0]
	return cmd.add(cobraCmd, provider)
}

func (cmd *addCommand) add(_ *cobra.Command, provider string) error {
	if addGeteuid() != 0 {
		return fmt.Errorf("adding a provider requires root privileges. Try again using sudo")
	}

	var name, baseURL string

	switch provider {
	case providerOpenAI:
		name = cmd.name
		if name == "" {
			name = "openai"
		}
		baseURL = defaultOpenAIBaseURL
	default:
		return fmt.Errorf("unsupported provider %q", provider)
	}

	exists, err := cmd.ctx.Config.ProviderExists(name)
	if err != nil {
		return fmt.Errorf("validating provider uniqueness: %w", err)
	}
	if exists {
		return fmt.Errorf("a provider with the name %q is already configured", name)
	}

	fmt.Printf("You are setting up a new authorization token for future requests to %s.\n", name)

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Printf("Enter API Key: ")
		_, err := fmt.Scanln(&apiKey)
		if err != nil {
			return fmt.Errorf("reading API key: %w", err)
		}
	}
	apiKey = strings.TrimSpace(apiKey)

	if err := cmd.ctx.Config.AddOpenAIProvider(name, baseURL, &apiKey); err != nil {
		return fmt.Errorf("adding provider: %w", err)
	}
	return nil
}
