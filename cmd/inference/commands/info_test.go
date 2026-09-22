package commands

import (
	"strings"
	"testing"

	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func TestInfoRendering(t *testing.T) {
	tests := []struct {
		name     string
		provider providers.Provider
		format   string
		want     string
	}{
		{
			name: "fully configured provider [yaml]",
			provider: providers.Provider{
				Name:    "gemma4",
				Type:    providers.TypeInferenceSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://localhost:8336/v1",
			},
			format: "yaml",
			want: `name: gemma4
type: inference-snap
state: enabled
api:
  openai:
    base-url: http://localhost:8336/v1
`,
		},
		{
			name: "fully configured provider [json]",
			provider: providers.Provider{
				Name:    "gemma4",
				Type:    providers.TypeInferenceSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://localhost:8336/v1",
			},
			format: "json",
			want: `{
  "name": "gemma4",
  "type": "inference-snap",
  "state": "enabled",
  "api": {
    "openai": {
      "base-url": "http://localhost:8336/v1"
    }
  }
}
`,
		},
		{
			name: "redact BaseURL with credentials and query [yaml]",
			provider: providers.Provider{
				Name:    "gemma4",
				Type:    providers.TypeInferenceSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://user:token@localhost:8336/v1?api_key=secret",
			},
			format: "yaml",
			want: `name: gemma4
type: inference-snap
state: enabled
api:
  openai:
    base-url: http://localhost:8336/v1
`,
		},
		{
			name: "provider without a BaseURL [yaml]",
			provider: providers.Provider{
				Name:  "gemma4",
				Type:  providers.TypeInferenceSnap,
				State: providers.StateEnabled,
			},
			format: "yaml",
			want: `name: gemma4
type: inference-snap
state: enabled
`,
		},
		{
			name: "provider without a BaseURL [json]",
			provider: providers.Provider{
				Name:  "gemma4",
				Type:  providers.TypeInferenceSnap,
				State: providers.StateEnabled,
			},
			format: "json",
			want: `{
  "name": "gemma4",
  "type": "inference-snap",
  "state": "enabled"
}
`,
		},
	}

	cmd := &infoCommand{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cmd.renderInfo(tt.provider, tt.format)
			if err != nil {
				t.Fatalf("error marshaling data: %q", err)
			}
			if got != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestInfoRendering_InvalidBaseURLReturnsError(t *testing.T) {
	provider := providers.Provider{
		Name:    "gemma4",
		Type:    providers.TypeInferenceSnap,
		State:   providers.StateEnabled,
		BaseURL: "http://[invalid",
	}

	cmd := &infoCommand{}
	if _, err := cmd.renderInfo(provider, "yaml"); err == nil {
		t.Fatal("expected an error for an unparseable BaseURL, got none")
	}
}

func TestInfo_PositionalArgsAreRequired(t *testing.T) {
	ctx, stdout, _ := newTestContext()

	if err := execute(Info(ctx)); err == nil {
		t.Fatal("expected error when no provider name is given")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for a missing argument, got %q", stdout.String())
	}
}

func TestInfo_TooManyPositionalArgsAreRejected(t *testing.T) {
	ctx, stdout, _ := newTestContext()

	if err := execute(Info(ctx), "gemma4", "qwen3"); err == nil {
		t.Fatal("expected error for more than one positional argument")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for unexpected positional arguments, got %q", stdout.String())
	}
}

func TestInfo_PrintsProvider(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "yaml",
			args: []string{"gemma4"},
			want: "name: gemma4\ntype: inference-snap\nstate: enabled\n",
		},
		{
			name: "json",
			args: []string{"gemma4", "--format=json"},
			want: "{\n  \"name\": \"gemma4\",\n  \"type\": \"inference-snap\",\n  \"state\": \"enabled\"\n}\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, _ := newTestContext()
			ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[
				{"snap":"gemma4","model_name":"Gemma 4","full_name":"canonical/gemma4","html_url":"https://example.com/gemma4"}
			]`)
			ctx.SnapdClient, _ = snapd.NewFakeServer(t, map[string]string{"gemma4": snapd.SnapStatusActive})

			if err := execute(Info(ctx), test.args...); err != nil {
				t.Fatalf("Info: %v", err)
			}
			if stdout.String() != test.want {
				t.Errorf("got:\n%q\nwant:\n%q", stdout.String(), test.want)
			}
		})
	}
}

func TestInfo_RejectsInvalidFormat(t *testing.T) {
	ctx, stdout, _ := newTestContext()

	err := execute(Info(ctx), "gemma4", "--format=xml")
	if err == nil {
		t.Fatal("expected invalid format to be rejected")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for an invalid format, got %q", stdout.String())
	}
}

func TestInfo_UnknownProvider(t *testing.T) {
	ctx, stdout, _ := newTestContext()
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[]`)
	ctx.SnapdClient, _ = snapd.NewFakeServer(t, nil)

	err := execute(Info(ctx), "some-imaginary-provider")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "some-imaginary-provider") {
		t.Fatalf("expected error to mention the requested name, got: %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output on error, got %q", stdout.String())
	}
}
