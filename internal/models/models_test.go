package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/providers"
)

func TestListProviderModelsAggregatesHealthyProviders(t *testing.T) {
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
		{Name: "gemma4", Type: providers.TypeInferenceSnap, State: providers.StateEnabled, BaseURL: first.URL + "/v1"},
		{Name: "broken", Type: providers.TypeInferenceSnap, State: providers.StateEnabled, BaseURL: second.URL + "/v1"},
		{Name: "openai", Type: providers.TypeOpenAI, BaseURL: third.URL + "/v1"},
		{Name: "unavailable", Type: providers.TypeInferenceSnap},
		{Name: "disabled", Type: providers.TypeInferenceSnap, State: providers.StateDisabled, BaseURL: second.URL + "/v1"},
	}
	got, err := listProviderModels(context.Background(), list, first.Client())
	if err != nil {
		t.Fatal(err)
	}
	want := []Model{
		{Name: "gemma4-e2b", Provider: "gemma4 snap"},
		{Name: "gemma4-e4b", Provider: "gemma4 snap"},
		{Name: "gpt4.5", Provider: "openai (remote)"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("model %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListProviderModelsReturnsEmptyWithoutAvailableProviders(t *testing.T) {
	got, err := listProviderModels(context.Background(), nil, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want empty list", got)
	}
}

func TestListProviderModelsFailsWhenEveryProviderIsBroken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := listProviderModels(context.Background(), []providers.Provider{
		{Name: "broken", Type: providers.TypeOpenAI, BaseURL: server.URL + "/v1"},
	}, server.Client())
	if err == nil || !strings.Contains(err.Error(), "all providers failed") {
		t.Fatalf("got error %v, want all providers failed", err)
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
