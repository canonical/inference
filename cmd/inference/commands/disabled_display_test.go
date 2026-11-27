package commands

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProvidersListsDisabledProviders(t *testing.T) {
	f := newDisabledFixture(t)

	if err := execute(Providers(f.ctx), "--format=json"); err != nil {
		t.Fatal(err)
	}

	var got providersJSONOutput
	if err := json.Unmarshal([]byte(f.stdout()), &got); err != nil {
		t.Fatal(err)
	}
	want := []providerJSON{
		{Name: "gemma4", Backend: "snap", Enabled: true},
		{Name: "qwen3", Backend: "snap", Enabled: false},
	}
	if !reflect.DeepEqual(got.Providers, want) {
		t.Fatalf("providers = %+v, want %+v", got.Providers, want)
	}
}

func TestInfoWorksForDisabledProvider(t *testing.T) {
	f := newDisabledFixture(t)

	if err := execute(Info(f.ctx), "qwen3", "--format=json"); err != nil {
		t.Fatal(err)
	}

	var got infoOutput
	if err := json.Unmarshal([]byte(f.stdout()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "qwen3" || got.State != "disabled" {
		t.Fatalf("got name %q state %q, want qwen3 disabled", got.Name, got.State)
	}
}

func TestInfoTableShowsDisabledState(t *testing.T) {
	f := newDisabledFixture(t)

	if err := execute(Info(f.ctx), "qwen3"); err != nil {
		t.Fatal(err)
	}

	if got, want := f.stdout(), "qwen3 [snap] - disabled ✗\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
