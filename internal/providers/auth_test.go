package providers

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

const testAPIKey = "sk-test-secret"

func TestAuthorizeRequestSetsBearer(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "http://provider.test/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}

	AuthorizeRequest(request, testAPIKey)

	if got := request.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q", got)
	}
}

func TestAuthorizeRequestWithoutKeyLeavesRequestUnchanged(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "http://provider.test/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}

	AuthorizeRequest(request, "")

	if got := request.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

func TestStripCredentialsRemovesAuthorizationHeader(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Authorization": {"Bearer " + testAPIKey},
			"X-Other":       {"kept"},
		},
	}

	StripCredentials(response)

	if _, exists := response.Header["Authorization"]; exists {
		t.Error("Authorization header not removed")
	}
	if got := response.Header.Get("X-Other"); got != "kept" {
		t.Errorf("X-Other = %q, want kept", got)
	}
}

func TestAPIKeyLookupGet(t *testing.T) {
	var nilLookup APIKeyLookup
	if key, err := nilLookup.Get("any"); key != "" || err != nil {
		t.Errorf("nil lookup = %q, %v", key, err)
	}

	failing := APIKeyLookup(func(string) (string, error) { return "", errors.New("denied") })
	if _, err := failing.Get("openai"); err == nil || !strings.Contains(err.Error(), `"openai"`) {
		t.Errorf("err = %v", err)
	}
}
