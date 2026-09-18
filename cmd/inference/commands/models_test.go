package commands

import (
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/models"
	"github.com/canonical/inference/internal/snapcatalog"
)

func TestModelsOutput(t *testing.T) {
	list := []models.Model{
		{PublicID: "openai/gpt4.5"},
		{PublicID: "gemma4/gemma4-e2b"},
	}
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "table",
			format: "table",
			want: "ID\n" +
				"openai/gpt4.5\n" +
				"gemma4/gemma4-e2b\n",
		},
		{
			name:   "json",
			format: "json",
			want: "{\n" +
				"  \"models\": [\n" +
				"    {\n" +
				"      \"id\": \"openai/gpt4.5\"\n" +
				"    },\n" +
				"    {\n" +
				"      \"id\": \"gemma4/gemma4-e2b\"\n" +
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
		{name: "table", want: "ID\n"},
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
	for _, format := range []string{"xml", "yaml"} {
		if err := execute(Models(&common.Context{}), "--format="+format); err == nil {
			t.Fatalf("expected %s format to be rejected", format)
		}
	}
}

func TestModelsEmptyOutput(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		got, err := renderModels(nil, format)
		if err != nil {
			t.Fatal(err)
		}
		want := "ID\n"
		if format == "json" {
			want = "{\n  \"models\": []\n}\n"
		}
		if got != want {
			t.Fatalf("%s output = %q, want %q", format, got, want)
		}
	}
}
