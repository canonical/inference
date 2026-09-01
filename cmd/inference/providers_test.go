package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/providers"
	"github.com/spf13/cobra"
)

type fakeRegistry struct {
	definitions []providers.Definition
	warnings    []string
	err         error
	called      bool
}

func (f *fakeRegistry) List(context.Context) ([]providers.Definition, []string, error) {
	f.called = true
	return f.definitions, f.warnings, f.err
}

type fakeStatusSource struct {
	statuses map[string]string
	err      error
	called   bool
}

func (f *fakeStatusSource) Statuses(context.Context) (map[string]string, error) {
	f.called = true
	return f.statuses, f.err
}

func definitions(names ...string) []providers.Definition {
	result := make([]providers.Definition, len(names))
	for i, name := range names {
		result[i] = providers.Definition{Name: name, Type: providers.TypeInferenceSnap}
	}
	return result
}

func newTestContext(registry *fakeRegistry, statuses *fakeStatusSource) (*Context, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	ctx := &Context{
		Stdout: &stdout,
		Stderr: &stderr,
		Providers: providers.NewService(
			registry,
			map[providers.Type]providers.StatusSource{
				providers.TypeInferenceSnap: statuses,
			},
		),
	}
	return ctx, &stdout, &stderr
}

func execute(cmd *cobra.Command, args ...string) error {
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestProviders_TableExactOutput(t *testing.T) {
	list := []providers.Provider{
		{Name: "gemma4", Type: providers.TypeInferenceSnap, Status: "active"},
		{Name: "qwen3", Type: providers.TypeInferenceSnap, Status: providers.StatusNotInstalled},
	}
	got, err := renderProvidersTable(list)
	if err != nil {
		t.Fatalf("render table: %v", err)
	}

	want := "PROVIDER  TYPE            STATUS\n" +
		"gemma4    inference-snap  active\n" +
		"qwen3     inference-snap  not installed\n" +
		"\n" +
		`Hint: run "inference install <provider>" to install providers.` + "\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestProviders_TableNoHintWhenAllInstalled(t *testing.T) {
	registry := &fakeRegistry{definitions: definitions("gemma4")}
	statuses := &fakeStatusSource{statuses: map[string]string{"gemma4": "active"}}
	ctx, stdout, _ := newTestContext(registry, statuses)

	if err := execute(Providers(ctx)); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if strings.Contains(stdout.String(), "Hint:") {
		t.Fatalf("did not expect a hint, got:\n%s", stdout.String())
	}
}

func TestProviders_JSONExactOutput(t *testing.T) {
	list := []providers.Provider{
		{Name: "gemma4", Type: providers.TypeInferenceSnap, Status: "active"},
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

func TestProviders_InstalledFlagFiltersTableAndJSON(t *testing.T) {
	registry := &fakeRegistry{definitions: definitions("gemma4", "qwen3")}
	statuses := &fakeStatusSource{statuses: map[string]string{"gemma4": "active"}}
	ctx, stdout, _ := newTestContext(registry, statuses)

	if err := execute(Providers(ctx), "--installed"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(stdout.String(), "qwen3") {
		t.Fatalf("expected qwen3 excluded, got:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Hint:") {
		t.Fatalf("did not expect a hint with --installed, got:\n%s", stdout.String())
	}
}

func TestProviders_JSONStdoutValidWithStderrWarnings(t *testing.T) {
	registry := &fakeRegistry{definitions: definitions("gemma4"), warnings: []string{"using stale cache"}}
	statuses := &fakeStatusSource{statuses: map[string]string{"gemma4": "active"}}
	ctx, stdout, stderr := newTestContext(registry, statuses)

	if err := execute(Providers(ctx), "--format=json"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.HasPrefix(stdout.String(), "{") {
		t.Fatalf("stdout must remain valid JSON, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "using stale cache") {
		t.Fatalf("expected warning on stderr, got %q", stderr.String())
	}
}

func TestProviders_InvalidFormatDoesNotCallDependencies(t *testing.T) {
	registry := &fakeRegistry{definitions: definitions("gemma4")}
	statuses := &fakeStatusSource{statuses: map[string]string{}}
	ctx, _, _ := newTestContext(registry, statuses)

	err := execute(Providers(ctx), "--format=xml")
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
	if registry.called || statuses.called {
		t.Fatal("expected no dependency calls for an invalid format")
	}
}

func TestProviders_PositionalArgsRejectedBeforeDependencies(t *testing.T) {
	registry := &fakeRegistry{definitions: definitions("gemma4")}
	statuses := &fakeStatusSource{statuses: map[string]string{}}
	ctx, _, _ := newTestContext(registry, statuses)

	err := execute(Providers(ctx), "unexpected-arg")
	if err == nil {
		t.Fatal("expected error for unexpected positional argument, got nil")
	}
	if registry.called || statuses.called {
		t.Fatal("expected no dependency calls for unexpected positional arguments")
	}
}

func TestProviders_DependencyErrorProducesNoPartialStdout(t *testing.T) {
	registry := &fakeRegistry{err: context.DeadlineExceeded}
	statuses := &fakeStatusSource{statuses: map[string]string{}}
	ctx, stdout, _ := newTestContext(registry, statuses)

	if err := execute(Providers(ctx)); err == nil {
		t.Fatal("expected error, got nil")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output on failure, got %q", stdout.String())
	}
}
