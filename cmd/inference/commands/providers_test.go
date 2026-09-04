package commands

import (
	"bytes"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/spf13/cobra"
)

func newTestContext() (*common.Context, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return &common.Context{Stdout: &stdout, Stderr: &stderr}, &stdout, &stderr
}

func execute(cmd *cobra.Command, args ...string) error {
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestProviders_TableExactOutput(t *testing.T) {
	list := []providers.Provider{
		{Name: "gemma4", Type: providers.TypeInferenceSnap, State: "active"},
		{Name: "qwen3", Type: providers.TypeInferenceSnap, State: providers.StateNotInstalled},
	}
	got, err := renderProvidersTable(list)
	if err != nil {
		t.Fatalf("render table: %v", err)
	}

	want := "PROVIDER  TYPE            STATE\n" +
		"gemma4    inference-snap  active\n" +
		"qwen3     inference-snap  not installed\n" +
		"\n" +
		`Hint: run "inference install <provider>" to install providers.` + "\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestProviders_TableNoHintWhenAllInstalled(t *testing.T) {
	list := []providers.Provider{{Name: "gemma4", Type: providers.TypeInferenceSnap, State: "active"}}
	got, err := renderProvidersTable(list)
	if err != nil {
		t.Fatalf("render table: %v", err)
	}
	if bytes.Contains([]byte(got), []byte("Hint:")) {
		t.Fatalf("did not expect a hint, got:\n%s", got)
	}
}

func TestProviders_JSONExactOutput(t *testing.T) {
	list := []providers.Provider{
		{Name: "gemma4", Type: providers.TypeInferenceSnap, State: "active"},
	}
	got, err := renderProvidersJSON(list)
	if err != nil {
		t.Fatalf("render JSON: %v", err)
	}

	want := `{
  "providers": [
    {
      "provider": "gemma4",
      "type": "inference-snap",
      "status": "active"
    }
  ]
}
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestProviders_JSONEmptyUsesEmptyArray(t *testing.T) {
	got, err := renderProvidersJSON(nil)
	if err != nil {
		t.Fatalf("render JSON: %v", err)
	}

	want := "{\n  \"providers\": []\n}\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestProviders_InvalidFormatIsRejectedBeforeListing(t *testing.T) {
	ctx, stdout, _ := newTestContext()

	err := execute(Providers(ctx), "--format=xml")
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for an invalid format, got %q", stdout.String())
	}
}

func TestProviders_PositionalArgsAreRejected(t *testing.T) {
	ctx, stdout, _ := newTestContext()

	err := execute(Providers(ctx), "unexpected-arg")
	if err == nil {
		t.Fatal("expected error for unexpected positional argument, got nil")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for unexpected positional arguments, got %q", stdout.String())
	}
}
