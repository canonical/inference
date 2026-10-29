package commands

import (
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/canonical/inference/internal/config"
	"github.com/spf13/cobra"
)

func TestAdd_ProviderCompletion(t *testing.T) {
	for _, test := range []struct {
		name   string
		args   []string
		prefix string
		want   []string
	}{
		{name: "all providers", want: []string{"openai"}},
		{name: "provider prefix", prefix: "op", want: []string{"openai"}},
		{name: "after provider", args: []string{"openai"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, directive := completeAddProvider(nil, test.args, test.prefix)
			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Fatalf("directive = %v, want no file completion", directive)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("completions = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAdd_Provider(t *testing.T) {
	setAddEffectiveUID(t, 0)

	for _, test := range []struct {
		name        string
		args        []string
		wantName    string
		apiKey      string
		wantKey     string
		wantMessage string
	}{
		{
			name:        "vendor provider",
			args:        []string{"openai"},
			wantName:    "openai",
			apiKey:      "openai-key",
			wantKey:     "openai-key",
			wantMessage: "You are setting up a new authorization token for future requests to openai.\n",
		},
		{
			name:        "vendor provider with name",
			args:        []string{"openai", "--name=openai-work"},
			wantName:    "openai-work",
			apiKey:      " work-key ",
			wantKey:     "work-key",
			wantMessage: "You are setting up a new authorization token for future requests to openai-work.\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SNAP_DATA", t.TempDir())
			t.Setenv("OPENAI_API_KEY", test.apiKey)
			ctx, _, _ := newTestContext(t)

			output, err := captureAddStdout(func() error {
				return execute(Add(ctx), test.args...)
			})
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if output != test.wantMessage {
				t.Fatalf("stdout = %q, want %q", output, test.wantMessage)
			}

			providers, err := ctx.Config.GetProviders()
			if err != nil {
				t.Fatalf("GetProviders: %v", err)
			}
			provider, exists := providers[test.wantName]
			if !exists {
				t.Fatalf("provider %q was not added", test.wantName)
			}
			if provider.Backend != config.BackendProxy ||
				provider.BaseURL != defaultOpenAIBaseURL ||
				provider.API == nil ||
				*provider.API != "openai" {
				t.Fatalf("provider = %+v, want OpenAI-compatible proxy at %q", provider, defaultOpenAIBaseURL)
			}

			key, err := ctx.Config.GetProviderKey(test.wantName)
			if err != nil {
				t.Fatalf("GetProviderKey: %v", err)
			}
			if key == nil || *key != test.wantKey {
				t.Fatalf("provider key = %v, want %q", key, test.wantKey)
			}
		})
	}
}

func TestAdd_PromptsForAPIKey(t *testing.T) {
	setAddEffectiveUID(t, 0)
	t.Setenv("SNAP_DATA", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	ctx, _, _ := newTestContext(t)

	oldStdin := os.Stdin
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdin pipe: %v", err)
	}
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = stdin.Close()
	})
	if _, err := input.WriteString(" prompted-key \n"); err != nil {
		t.Fatalf("write prompted key: %v", err)
	}
	if err := input.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}

	output, err := captureAddStdout(func() error {
		return execute(Add(ctx), "openai")
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	wantOutput := "You are setting up a new authorization token for future requests to openai.\nEnter API Key: "
	if output != wantOutput {
		t.Fatalf("stdout = %q, want %q", output, wantOutput)
	}
	key, err := ctx.Config.GetProviderKey("openai")
	if err != nil {
		t.Fatalf("GetProviderKey: %v", err)
	}
	if key == nil || *key != "prompted-key" {
		t.Fatalf("provider key = %v, want prompted-key", key)
	}
}

func TestAdd_RejectsUnsupportedAndDuplicateProviders(t *testing.T) {
	setAddEffectiveUID(t, 0)

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "unsupported provider", args: []string{"custom"}, want: `unsupported provider "custom"`},
		{name: "duplicate default name", args: []string{"openai"}, want: `a provider with the name "openai" is already configured`},
		{name: "duplicate custom name", args: []string{"openai", "--name=openai-work"}, want: `a provider with the name "openai-work" is already configured`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SNAP_DATA", t.TempDir())
			t.Setenv("OPENAI_API_KEY", "new-key")
			ctx, _, _ := newTestContext(t)
			if test.name != "unsupported provider" {
				if err := ctx.Config.AddOpenAIProvider(test.args[0], defaultOpenAIBaseURL, new("existing-key")); err != nil {
					t.Fatalf("seed provider: %v", err)
				}
				if test.name == "duplicate custom name" {
					if err := ctx.Config.AddOpenAIProvider("openai-work", defaultOpenAIBaseURL, new("existing-key")); err != nil {
						t.Fatalf("seed named provider: %v", err)
					}
				}
			}

			err := execute(Add(ctx), test.args...)
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if test.name != "unsupported provider" {
				name := "openai"
				if test.name == "duplicate custom name" {
					name = "openai-work"
				}
				key, err := ctx.Config.GetProviderKey(name)
				if err != nil {
					t.Fatalf("GetProviderKey: %v", err)
				}
				if key == nil || *key != "existing-key" {
					t.Fatalf("provider key = %v, want existing-key", key)
				}
			}
		})
	}
}

func TestAdd_RequiresRoot(t *testing.T) {
	setAddEffectiveUID(t, 1000)

	output, err := captureAddStdout(func() error {
		return execute(Add(nil), "openai")
	})
	want := "adding a provider requires root privileges. Try again using sudo"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if output != "" {
		t.Fatalf("stdout = %q, want no output before privilege check", output)
	}
}

func setAddEffectiveUID(t *testing.T, uid int) {
	t.Helper()
	previous := addGeteuid
	addGeteuid = func() int { return uid }
	t.Cleanup(func() { addGeteuid = previous })
}

func captureAddStdout(run func() error) (string, error) {
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = writer
	runErr := run()
	os.Stdout = oldStdout
	if err := writer.Close(); err != nil {
		_ = reader.Close()
		return "", err
	}
	output, readErr := io.ReadAll(reader)
	if err := reader.Close(); err != nil {
		return "", err
	}
	if readErr != nil {
		return "", readErr
	}
	return string(output), runErr
}
