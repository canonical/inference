package models

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/providers"
)

type staticProviders struct {
	providers.Service
	list []providers.Provider
	err  error
}

func (p staticProviders) List(context.Context) ([]providers.Provider, error) {
	return p.list, p.err
}

func newService(list []providers.Provider, client *http.Client) Service {
	return NewService(staticProviders{list: list}, client)
}

func TestListReturnsProviderListingError(t *testing.T) {
	listErr := errors.New("snapd unavailable")
	service := NewService(staticProviders{err: listErr}, http.DefaultClient)

	_, err := service.List(context.Background())
	if !errors.Is(err, listErr) {
		t.Fatalf("got error %v, want %v", err, listErr)
	}
}

func TestListReportsModelsAndErrorsPerProvider(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gemma4-e4b"},{"id":"gemma4-e2b"},{"id":"gemma4-e2b"},{"id":""}]}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer second.Close()
	third := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt4.5"}]}`))
	}))
	defer third.Close()

	list := []providers.Provider{
		{Name: "gemma4", Backend: providers.BackendSnap, State: providers.StateEnabled, BaseURL: first.URL + "/v1"},
		{Name: "broken", Backend: providers.BackendSnap, State: providers.StateEnabled, BaseURL: second.URL + "/v1"},
		{Name: "openai", Backend: providers.BackendProxy, BaseURL: third.URL + "/v1"},
		{Name: "unavailable", Backend: providers.BackendSnap},
		{Name: "not-connected", Backend: providers.BackendSnap, State: providers.StateEnabled, Connection: providers.ConnectionNotConnected},
		{Name: "disabled", Backend: providers.BackendSnap, State: providers.StateDisabled, BaseURL: second.URL + "/v1"},
	}
	listedProviders := append(list, providers.Provider{
		Name: "socket-only", Backend: providers.BackendSnap, State: providers.StateUnknown, Connection: providers.ConnectionConnected,
		Err: errors.New("OPENAI_BASE_URL is empty"),
	})
	got, err := newService(listedProviders, first.Client()).List(context.Background())
	if err != nil {
		t.Fatal("expected no error, got", err)
	}
	want := []ProviderModels{
		{
			ProviderName: "broken",
			Models:       []Model{},
			Err:          errors.New("requesting models: upstream returned 500 Internal Server Error"),
		},
		{
			ProviderName: "gemma4",
			Models: []Model{
				{PublicID: "gemma4/gemma4-e2b", NativeID: "gemma4-e2b", ProviderName: "gemma4", ProviderBaseURL: first.URL + "/v1"},
				{PublicID: "gemma4/gemma4-e4b", NativeID: "gemma4-e4b", ProviderName: "gemma4", ProviderBaseURL: first.URL + "/v1"},
			},
			Err: nil,
		},
		{
			ProviderName: "openai",
			Models: []Model{
				{PublicID: "openai/gpt4.5", NativeID: "gpt4.5", ProviderName: "openai", ProviderBaseURL: third.URL + "/v1"},
			},
			Err: nil,
		},
		{
			ProviderName: "socket-only",
			Models:       []Model{},
			Err:          errors.New("OPENAI_BASE_URL is empty"),
		},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].ProviderName != want[i].ProviderName {
			t.Errorf("Provider name mismatch: got %q at index %d, want %q", got[i].ProviderName, i, want[i].ProviderName)
		}
		if got[i].Err != nil && want[i].Err == nil {
			t.Errorf("unexpected error at index %d: got %v, want nil", i, got[i].Err)
		} else if got[i].Err == nil && want[i].Err != nil {
			t.Errorf("missing expected error at index %d: got nil, want %v", i, want[i].Err)
		}
		if len(got[i].Models) != len(want[i].Models) {
			t.Errorf("model count mismatch at index %d: got %d, want %d", i, len(got[i].Models), len(want[i].Models))
			continue
		}
		for j := range want[i].Models {
			if got[i].Models[j] != want[i].Models[j] {
				t.Errorf("model %d.%d = %+v, want %+v", i, j, got[i].Models[j], want[i].Models[j])
			}
		}
	}
}

func TestListReportsInvalidBaseURLsPerProvider(t *testing.T) {
	list := []providers.Provider{
		{Name: "bad-scheme", Backend: providers.BackendSnap, State: providers.StateEnabled, BaseURL: "ftp://user:secret@example.com/v1"},
		{Name: "unparsable", Backend: providers.BackendSnap, State: providers.StateEnabled, BaseURL: "http://user:secret@exa mple.com/v1"},
	}
	got, err := newService(list, http.DefaultClient).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(list) {
		t.Fatalf("got %+v, want %d providers", got, len(list))
	}
	for _, providerModels := range got {
		if providerModels.Err == nil {
			t.Errorf("provider %q: expected error", providerModels.ProviderName)
		} else if strings.Contains(providerModels.Err.Error(), "secret") {
			t.Errorf("provider %q: error leaks credentials: %v", providerModels.ProviderName, providerModels.Err)
		}
	}
}

func TestListPreservesProviderErrorsWithoutRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected request for a provider with a configuration error")
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	providerErr := errors.New("invalid configuration")
	got, err := newService([]providers.Provider{
		{
			Name: "broken", Backend: providers.BackendSnap, State: providers.StateEnabled, BaseURL: server.URL,
			Err: providerErr,
		},
	}, server.Client()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProviderName != "broken" || got[0].Err != providerErr {
		t.Fatalf("got %+v, want the original provider error", got)
	}
	if got[0].Models == nil || len(got[0].Models) != 0 {
		t.Fatalf("got models %#v, want an empty list", got[0].Models)
	}
}

func TestListReturnsEmptyWithoutAvailableProviders(t *testing.T) {
	got, err := newService(nil, http.DefaultClient).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want empty list", got)
	}
}

func TestListReturnsProviderErrorForBrokenProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer server.Close()

	got, err := newService([]providers.Provider{
		{Name: "broken", Backend: providers.BackendProxy, BaseURL: server.URL + "/v1"},
	}, server.Client()).List(context.Background())

	if err != nil {
		t.Fatalf("got unexpected error %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("got %d providers, want 1", len(got))
	}

	if got[0].ProviderName != "broken" {
		t.Fatalf("got provider name %q, want %q", got[0].ProviderName, "broken")
	}

	if got[0].Err == nil {
		t.Fatalf("expected error for broken provider, got nil")
	}

	if len(got[0].Models) != 0 {
		t.Fatalf("expected no models for broken provider, got %d", len(got[0].Models))
	}
}

func TestFetchModelsRejectsMissingData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list"}`))
	}))
	defer server.Close()

	_, err := fetchModels(context.Background(), server.Client(), server.URL+"/v1")
	if err == nil || !strings.Contains(err.Error(), "missing data array") {
		t.Fatalf("got error %v, want missing data array", err)
	}
}
