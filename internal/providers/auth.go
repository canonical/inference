package providers

import (
	"fmt"
	"net/http"
)

type APIKeyLookup func(providerName string) (string, error)

func (lookup APIKeyLookup) Get(providerName string) (string, error) {
	if lookup == nil {
		return "", nil
	}
	apiKey, err := lookup(providerName)
	if err != nil {
		return "", fmt.Errorf("reading API key for provider %q: %w", providerName, err)
	}
	return apiKey, nil
}

func AuthorizeRequest(request *http.Request, apiKey string) {
	if apiKey == "" {
		return
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
}

func StripCredentials(response *http.Response) {
	response.Header.Del("Authorization")
}
