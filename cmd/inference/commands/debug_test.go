package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/spf13/cobra"
)

func debugTestProviders(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "provider-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	providerDir := filepath.Join(root, "provider-2")
	if err := os.Mkdir(providerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "provider.env"), []byte("SNAP_NAME=gemma4\nUNIX_SOCKET=x.sock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func executeDebug(ctx *common.Context, args ...string) error {
	root := &cobra.Command{Use: "inference", SilenceUsage: true}
	root.SetOut(ctx.Stdout)
	root.SetErr(ctx.Stderr)
	root.AddCommand(Debug(ctx))
	return execute(root, append([]string{"debug"}, args...)...)
}

func TestDebugProvidersTable(t *testing.T) {
	root := debugTestProviders(t)
	ctx, stdout, stderr := newTestContext()
	ctx.ShareProvidersPath = root

	if err := executeDebug(ctx, "providers"); err != nil {
		t.Fatal(err)
	}

	want := "ID  DETAILS\n" +
		"1   Cannot use shared provider: missing provider.env in " + filepath.Join(root, "provider-1") + "/\n" +
		"2   Cannot use shared provider: missing OPENAI_BASE_URL in " + filepath.Join(root, "provider-2", "provider.env") + "\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout:\n%q\nwant:\n%q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
}

func TestDebugProvidersJSON(t *testing.T) {
	root := debugTestProviders(t)
	ctx, stdout, _ := newTestContext()
	ctx.ShareProvidersPath = root

	if err := executeDebug(ctx, "providers", "--format=json"); err != nil {
		t.Fatal(err)
	}

	want := `{
  "issues": [
    {
      "id": 1,
      "details": "Cannot use shared provider: missing provider.env in ` + filepath.Join(root, "provider-1") + `/"
    },
    {
      "id": 2,
      "details": "Cannot use shared provider: missing OPENAI_BASE_URL in ` + filepath.Join(root, "provider-2", "provider.env") + `"
    }
  ]
}
`
	if got := stdout.String(); got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

func TestDebugProvidersWithoutIssues(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		wantStdout string
		wantStderr string
	}{
		{name: "table", args: []string{"providers"}, wantStderr: "No provider issues found.\n"},
		{name: "json", args: []string{"providers", "--format=json"}, wantStdout: "{\n  \"issues\": []\n}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext()
			ctx.ShareProvidersPath = t.TempDir()

			if err := executeDebug(ctx, test.args...); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != test.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), test.wantStdout)
			}
			if stderr.String() != test.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestDebugProvidersRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"providers", "--format=xml"},
		{"providers", "unexpected"},
		{"unknown"},
	} {
		ctx, _, _ := newTestContext()
		if err := executeDebug(ctx, args...); err == nil {
			t.Errorf("expected %v to be rejected", args)
		}
	}
}
