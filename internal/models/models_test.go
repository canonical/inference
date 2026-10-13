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
	got, err := Discover(context.Background(), list, first.Client())
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

func TestListProviderModelsReturnsEmptyWithoutAvailableProviders(t *testing.T) {
	got, err := Discover(context.Background(), nil, http.DefaultClient)
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

	got, err := Discover(context.Background(), []providers.Provider{
		{Name: "broken", Type: providers.TypeOpenAI, BaseURL: server.URL + "/v1"},
	}, server.Client())

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
