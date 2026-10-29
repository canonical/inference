package commands

import (
	"strings"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/mattn/go-runewidth"
)

func TestInfoRendering(t *testing.T) {
	tests := []struct {
		name     string
		provider providers.Provider
		format   string
		want     string
	}{
		{
			name: "fully configured provider [json]",
			provider: providers.Provider{
				Name:    "gemma4",
				Backend: providers.BackendSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://localhost:8336/v1",
			},
			format: "json",
			want: `{
  "name": "gemma4",
  "backend": "snap",
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
			name: "redact BaseURL with credentials and query [json]",
			provider: providers.Provider{
				Name:    "gemma4",
				Backend: providers.BackendSnap,
				State:   providers.StateEnabled,
				BaseURL: "http://user:token@localhost:8336/v1?api_key=secret",
			},
			format: "json",
			want: `{
  "name": "gemma4",
  "backend": "snap",
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
			name: "provider without a BaseURL [json]",
			provider: providers.Provider{
				Name:    "gemma4",
				Backend: providers.BackendSnap,
				State:   providers.StateEnabled,
			},
			format: "json",
			want: `{
  "name": "gemma4",
  "backend": "snap",
  "state": "enabled"
}
`,
		},
		{
			name: "provider with 2 engines [json]",
			provider: providers.Provider{
				Name:    "gemma4",
				Backend: providers.BackendSnap,
				State:   providers.StateEnabled,
				Engines: []providers.Engine{
					{
						Name:         "cpu",
						Runtime:      "llamacpp",
						DefaultModel: "gemma4-e4b",
						Models:       []string{"gemma4-e2b", "gemma4-e4b"},
					},
					{
						Name:         "nvidia-gpu",
						Runtime:      "llamacpp-cuda",
						DefaultModel: "gemma4-e4b",
						Models:       []string{"gemma4-e4b", "gemma4-26b-a4b"},
					},
				},
			},
			format: "json",
			want: `{
  "name": "gemma4",
  "backend": "snap",
  "state": "enabled",
  "engines": [
    {
      "name": "cpu",
      "runtime": "llamacpp",
      "default-model": "gemma4-e4b",
      "models": [
        "gemma4-e2b",
        "gemma4-e4b"
      ]
    },
    {
      "name": "nvidia-gpu",
      "runtime": "llamacpp-cuda",
      "default-model": "gemma4-e4b",
      "models": [
        "gemma4-e4b",
        "gemma4-26b-a4b"
      ]
    }
  ]
}
`,
		},
		{
			name: "engine without a default model omits the field [json]",
			provider: providers.Provider{
				Name:    "gemma4",
				Backend: providers.BackendSnap,
				State:   providers.StateEnabled,
				Engines: []providers.Engine{
					{
						Name:    "cpu",
						Runtime: "llamacpp",
						Models:  []string{"gemma4-e2b", "gemma4-e4b"},
					},
				},
			},
			format: "json",
			want: `{
  "name": "gemma4",
  "backend": "snap",
  "state": "enabled",
  "engines": [
    {
      "name": "cpu",
      "runtime": "llamacpp",
      "models": [
        "gemma4-e2b",
        "gemma4-e4b"
      ]
    }
  ]
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
		Backend: providers.BackendSnap,
		State:   providers.StateEnabled,
		BaseURL: "http://[invalid",
	}

	cmd := &infoCommand{}
	if _, err := cmd.renderInfo(provider, "table"); err == nil {
		t.Fatal("expected an error for an unparseable BaseURL, got none")
	}
}

func TestInfo_PositionalArgsAreRequired(t *testing.T) {
	ctx, stdout, _ := newTestContext(t)

	if err := execute(Info(ctx)); err == nil {
		t.Fatal("expected error when no provider name is given")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for a missing argument, got %q", stdout.String())
	}
}

func TestInfo_TooManyPositionalArgsAreRejected(t *testing.T) {
	ctx, stdout, _ := newTestContext(t)

	if err := execute(Info(ctx), "gemma4", "qwen3"); err == nil {
		t.Fatal("expected error for more than one positional argument")
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output for unexpected positional arguments, got %q", stdout.String())
	}
}

func TestInfo_PrintsProvider(t *testing.T) {
	const catalog = `[
		{
			"snap": "gemma4",
			"model_name": "Gemma 4",
			"repo_url": "https://example.com/gemma4",
			"engines": [
				{
					"name": "amd-gpu",
					"runtime": "llamacpp-rocm",
					"default_model": "gemma4-e4b",
					"models": ["gemma4-e2b", "gemma4-e4b"]
				},
				{
					"name": "cpu",
					"runtime": "llamacpp",
					"default_model": "gemma4-e4b",
					"models": ["gemma4-e2b", "gemma4-e4b"]
				}
			]
		}
	]`

	for _, test := range []struct {
		name     string
		args     []string
		statuses map[string]string
		want     string
	}{
		{
			name:     "enabled [table]",
			args:     []string{"gemma4"},
			statuses: map[string]string{"gemma4": snapd.SnapStatusActive},
			want: "gemma4 [snap] - enabled ✓\n\n" +
				"ENGINE    gemma4-e2b  gemma4-e4b\n" +
				"amd-gpu   ✓           ✓\n" +
				"cpu       ✓           ✓\n",
		},
		{
			name:     "enabled [json]",
			args:     []string{"gemma4", "--format=json"},
			statuses: map[string]string{"gemma4": snapd.SnapStatusActive},
			want: `{
  "name": "gemma4",
  "backend": "snap",
  "state": "enabled",
  "engines": [
    {
      "name": "amd-gpu",
      "runtime": "llamacpp-rocm",
      "default-model": "gemma4-e4b",
      "models": [
        "gemma4-e2b",
        "gemma4-e4b"
      ]
    },
    {
      "name": "cpu",
      "runtime": "llamacpp",
      "default-model": "gemma4-e4b",
      "models": [
        "gemma4-e2b",
        "gemma4-e4b"
      ]
    }
  ]
}
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, _ := newTestContext(t)
			ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, catalog)
			ctx.SnapdClient, _ = snapd.NewFakeServer(t, test.statuses)

			if err := execute(Info(ctx), test.args...); err != nil {
				t.Fatalf("Info: %v", err)
			}
			if stdout.String() != test.want {
				t.Errorf("got:\n%q\nwant:\n%q", stdout.String(), test.want)
			}
		})
	}
}

func TestInfoRendering_Table(t *testing.T) {
	cmd := &infoCommand{Context: &common.Context{HTTPHost: "127.0.0.1", HTTPPort: "8400"}}
	provider := providers.Provider{
		Name:    "gemma4",
		Backend: providers.BackendSnap,
		State:   providers.StateEnabled,
		BaseURL: "http://localhost:8336/v1",
		Engines: []providers.Engine{
			{Name: "amd-gpu", Models: []string{"gemma4-e2b", "gemma4-e4b", "gemma4-12b", "gemma4-26b-a4b"}},
			{Name: "intel-gpu", Models: []string{"gemma4-e4b-ov"}},
		},
	}

	got, err := cmd.renderInfo(provider, "table")
	if err != nil {
		t.Fatalf("render table: %v", err)
	}
	for _, model := range []string{"gemma4-e2b", "gemma4-e4b", "gemma4-12b", "gemma4-26b-a4b"} {
		if !strings.Contains(got, model) {
			t.Errorf("expected model column %q, got:\n%s", model, got)
		}
	}
	if strings.Contains(got, "gemma4-e4b-ov") {
		t.Errorf("expected longer -ov model to be omitted, got:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "intel-gpu") && !strings.Contains(line, "✓") {
			t.Errorf("expected intel-gpu to match the retained shorter model, got %q", line)
		}
	}
}

func TestInfoRendering_TableWidth(t *testing.T) {
	cmd := &infoCommand{}
	provider := providers.Provider{
		Name:    "long-provider",
		Backend: providers.BackendSnap,
		State:   providers.StateEnabled,
		Engines: []providers.Engine{{
			Name:   strings.Repeat("long-engine-", 20),
			Models: []string{"long-provider-" + strings.Repeat("long-model-", 20)},
		}},
	}
	got, err := cmd.renderInfo(provider, "table")
	if err != nil {
		t.Fatalf("render table: %v", err)
	}
	table := strings.SplitN(got, "\n\n", 2)[1]
	for _, line := range strings.Split(strings.TrimSpace(table), "\n") {
		if width := runewidth.StringWidth(line); width > 120 {
			t.Errorf("table line width %d exceeds 120: %q", width, line)
		}
	}
}

func TestInfoRendering_PreservesParsedModelName(t *testing.T) {
	provider := providers.Provider{
		Name:    "glm-4-7-flash",
		Backend: providers.BackendSnap,
		State:   providers.StateEnabled,
		Engines: []providers.Engine{{
			Name:   "cpu",
			Models: []string{"glm-4.7-flash-30b-a3b"},
		}},
	}

	got, err := (&infoCommand{}).renderInfo(provider, "table")
	if err != nil {
		t.Fatalf("render table: %v", err)
	}
	if !strings.Contains(got, "glm-4.7-flash-30b-a3b") {
		t.Errorf("expected parsed model name to be preserved, got:\n%s", got)
	}
}

func TestInfoRendering_ProxyTable(t *testing.T) {
	cmd := &infoCommand{Context: &common.Context{HTTPHost: "127.0.0.1", HTTPPort: "8400"}}
	for _, test := range []struct {
		name  string
		state providers.LifecycleState
		want  string
	}{
		{
			name:  "enabled",
			state: providers.StateEnabled,
			want: "openai [proxy] - enabled ✓\n\n" +
				"API: http://127.0.0.1:8400/v1 (proxy)\n" +
				"     └── https://api.openai.com/v1 (upstream)\n",
		},
		{
			name:  "disabled",
			state: providers.StateDisabled,
			want:  "lemonade [proxy] - disabled ✗\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			name, baseURL := "openai", "https://api.openai.com/v1"
			if test.state == providers.StateDisabled {
				name, baseURL = "lemonade", "http://localhost:8080/v1"
			}
			got, err := cmd.renderInfo(providers.Provider{
				Name: name, Backend: providers.BackendProxy, State: test.state, BaseURL: baseURL,
			}, "table")
			if err != nil {
				t.Fatalf("render table: %v", err)
			}
			if got != test.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, test.want)
			}
		})
	}
}

func TestInfo_RejectsInvalidFormat(t *testing.T) {
	ctx, stdout, _ := newTestContext(t)

	for _, format := range []string{"xml", "yaml"} {
		err := execute(Info(ctx), "gemma4", "--format="+format)
		if err == nil {
			t.Errorf("expected format %q to be rejected", format)
		}
		if stdout.String() != "" {
			t.Fatalf("expected no stdout output for invalid format %q, got %q", format, stdout.String())
		}
	}
}

func TestInfo_UnknownProvider(t *testing.T) {
	ctx, stdout, _ := newTestContext(t)
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

func TestInfo_ProxyOpenAIBaseURL(t *testing.T) {
	cmd := &infoCommand{Context: &common.Context{HTTPHost: "0.0.0.0", HTTPPort: "8400"}}
	got := cmd.proxyOpenAIBaseURL()
	want := "http://localhost:8400/v1"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
