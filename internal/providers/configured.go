package providers

import "context"

// ConfiguredOpenAIProviders returns the list of configured OpenAI providers.
// It takes a context.Context as an argument to allow for cancellation of the operation.
func ConfiguredOpenAIProviders(ctx context.Context) ([]Provider, error) {
	// STUB: In a real implementation, this function would read the configuration for OpenAI providers
	// from the appropriate source and return the list of configured providers.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []Provider{}, nil
}
