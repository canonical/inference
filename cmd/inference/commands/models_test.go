package commands

import (
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/models"
	"github.com/canonical/inference/internal/snapcatalog"
)

func TestModelsOutput(t *testing.T) {
	list := []models.Model{
		{Name: "gpt4.5", Provider: "openai (remote)"},
		{Name: "gemma4-e2b", Provider: "gemma4 snap"},
	}
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "table",
			format: "table",
			want: "NAME        PROVIDER\n" +
				"gpt4.5      openai (remote)\n" +
				"gemma4-e2b  gemma4 snap\n",
		},
		{
			name:   "yaml",
			format: "yaml",
			want: "models:\n" +
				"  - name: gpt4.5\n" +
				"    provider: openai (remote)\n" +
				"  - name: gemma4-e2b\n" +
				"    provider: gemma4 snap\n",
		},
		{
			name:   "json",
			format: "json",
			want: "{\n" +
				"  \"models\": [\n" +
				"    {\n" +
				"      \"name\": \"gpt4.5\",\n" +
				"      \"provider\": \"openai (remote)\"\n" +
				"    },\n" +
				"    {\n" +
				"      \"name\": \"gemma4-e2b\",\n" +
				"      \"provider\": \"gemma4 snap\"\n" +
				"    }\n" +
				"  ]\n" +
				"}\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := renderModels(list, test.format)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, test.want)
			}
		})
	}
}

func TestModelsCommandOutputFormats(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "table", want: "NAME  PROVIDER\n"},
		{name: "yaml", args: []string{"--format=yaml"}, want: "models: []\n"},
		{name: "json", args: []string{"--format=json"}, want: "{\n  \"models\": []\n}\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext()
			ctx.SnapCatalog = &snapcatalog.Reader{}
			if err := execute(Models(ctx), test.args...); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != test.want {
				t.Fatalf("got:\n%s\nwant:\n%s", stdout.String(), test.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestModelsRejectsArguments(t *testing.T) {
	if err := execute(Models(&common.Context{}), "unexpected"); err == nil {
		t.Fatal("expected arguments to be rejected")
	}
}

func TestModelsRejectsInvalidFormat(t *testing.T) {
	if err := execute(Models(&common.Context{}), "--format=xml"); err == nil {
		t.Fatal("expected invalid format to be rejected")
	}
}

func TestModelsEmptyOutput(t *testing.T) {
	for _, format := range []string{"table", "yaml", "json"} {
		got, err := renderModels(nil, format)
		if err != nil {
			t.Fatal(err)
		}
		want := "NAME  PROVIDER\n"
		if format == "yaml" {
			want = "models: []\n"
		} else if format == "json" {
			want = "{\n  \"models\": []\n}\n"
		}
		if got != want {
			t.Fatalf("%s output = %q, want %q", format, got, want)
		}
	}
}
