package commands

import (
	"bytes"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/spf13/cobra"
)

func newTestContext() (*common.Context, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return &common.Context{
		Stdout:   &stdout,
		Stderr:   &stderr,
		HTTPHost: "127.0.0.1",
		HTTPPort: "8400",
	}, &stdout, &stderr
}

func execute(cmd *cobra.Command, args ...string) error {
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestProviders_TableExactOutput(t *testing.T) {
	list := []providers.Provider{
		{Name: "gemma4", Backend: providers.BackendSnap, State: providers.StateEnabled},
		{Name: "qwen3", Backend: providers.BackendSnap, State: providers.StateDisabled},
	}
	cmd := &providersCommand{}
	got, err := cmd.outputTable(list)
	if err != nil {
		t.Fatalf("render table: %v", err)
	}

	want := "PROVIDER  BACKEND  ENABLED\n" +
		"gemma4    snap     yes\n" +
		"qwen3     snap     no\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestProviders_TableHintWhenAllInstalled(t *testing.T) {
	ctx, stdout, stderr := newTestContext()
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[{"snap":"gemma4","model_name":"Gemma 4"}]`)
	ctx.SnapdClient, _ = snapd.NewFakeServer(t, map[string]string{
		"gemma4": snapd.SnapStatusActive,
	})
	if err := execute(Providers(ctx)); err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("gemma4")) {
		t.Fatalf("expected provider table on stdout, got:\n%s", stdout.String())
	}
	wantHint := `Hint: Run "inference find" to discover other inference snaps.` + "\n"
	if stderr.String() != wantHint {
		t.Fatalf("stderr = %q, want %q", stderr.String(), wantHint)
	}
}

func TestProviders_JSONExactOutput(t *testing.T) {
	list := []providers.Provider{
		{Name: "gemma4", Backend: providers.BackendSnap, State: providers.StateEnabled},
		{Name: "qwen3", Backend: providers.BackendSnap, State: providers.StateDisabled},
	}
	cmd := &providersCommand{}
	got, err := cmd.outputJSON(list)
	if err != nil {
		t.Fatalf("render JSON: %v", err)
	}

	want := `{
  "providers": [
    {
      "provider": "gemma4",
      "backend": "snap",
      "enabled": true
    },
    {
      "provider": "qwen3",
      "backend": "snap",
      "enabled": false
    }
  ]
}
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestProviders_JSONEmptyUsesEmptyArray(t *testing.T) {
	cmd := &providersCommand{}
	got, err := cmd.outputJSON(nil)
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

func TestProviders_OnlyInstalledProviders(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			ctx, stdout, _ := newTestContext()
			ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[
				{"snap":"gemma4","model_name":"Gemma 4"},
				{"snap":"qwen3","model_name":"Qwen 3"},
				{"snap":"smollm2","model_name":"SmolLM2"}
			]`)
			ctx.SnapdClient, _ = snapd.NewFakeServer(t, map[string]string{
				"gemma4": snapd.SnapStatusActive,
				"qwen3":  snapd.SnapStatusInstalled,
			})

			if err := execute(Providers(ctx), "--format="+format); err != nil {
				t.Fatalf("Providers: %v", err)
			}
			for _, name := range []string{"gemma4", "qwen3"} {
				if !bytes.Contains(stdout.Bytes(), []byte(name)) {
					t.Fatalf("expected installed provider %q in output, got:\n%s", name, stdout.String())
				}
			}
			if bytes.Contains(stdout.Bytes(), []byte("smollm2")) {
				t.Fatalf("did not expect catalog-only provider in output, got:\n%s", stdout.String())
			}
		})
	}
}

func TestProviders_NoInstalledProviders(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext()
			ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[{"snap":"gemma4","model_name":"Gemma 4"}]`)
			ctx.SnapdClient, _ = snapd.NewFakeServer(t, nil)

			if err := execute(Providers(ctx), "--format="+format); err != nil {
				t.Fatalf("Providers: %v", err)
			}
			want := "PROVIDER  BACKEND  ENABLED\n"
			wantHint := `Hint: Run "inference find" to discover other inference snaps.` + "\n"
			if format == "json" {
				want = "{\n  \"providers\": []\n}\n"
				wantHint = ""
			}
			if got := stdout.String(); got != want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, want)
			}
			if got := stderr.String(); got != wantHint {
				t.Fatalf("stderr = %q, want %q", got, wantHint)
			}
		})
	}
}
