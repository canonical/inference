package commands

import (
	"testing"

	"github.com/canonical/inference/internal/providers"
)

func TestInfoRendering(t *testing.T) {
	tests := []struct {
		name     string
		provider providers.Provider
		want     string
	}{
		{
			name: "fully configured provider",
			provider: providers.Provider{
				Name:    "gemma4",
				Type:    providers.TypeInferenceSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://localhost:8336/v1",
			},
			want: `name: gemma4
type: inference-snap
state: enabled
api
  openai
    base-url: http://localhost:8336/v1
`,
		},
		{
			name: "redact BaseURL with credentials and query",
			provider: providers.Provider{
				Name:    "gemma4",
				Type:    providers.TypeInferenceSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://user:token@localhost:8336/v1?api_key=secret",
			},
			want: `name: gemma4
type: inference-snap
state: enabled
api
  openai
    base-url: http://localhost:8336/v1
`,
		},
		{
			name: "provider without a BaseURL",
			provider: providers.Provider{
				Name:  "gemma4",
				Type:  providers.TypeInferenceSnap,
				State: providers.StateEnabled,
			},
			want: `name: gemma4
type: inference-snap
state: enabled
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderInfo(tt.provider)
			if got != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tt.want)
			}
		})
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
