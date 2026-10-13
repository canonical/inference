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

	cmd := &modelsCommand{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got string
			var err error
			if test.format == "json" {
				got, err = cmd.outputJSON(list)
			} else {
				got, err = cmd.outputTable(list)
			}
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
		name    string
		args    []string
		wantErr string
	}{
		{name: "table", wantErr: "no providers installed.\n\nHint: Run \"inference providers\" for details"},
		{name: "json", args: []string{"--format=json"}, wantErr: "no providers installed.\n\nHint: Run \"inference providers\" for details"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext()
			ctx.SnapCatalog = &snapcatalog.Reader{}
			err := execute(Models(ctx), test.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if err.Error() != test.wantErr {
				t.Fatalf("expected error %q, got %q", test.wantErr, err)
			}

			if stdout.Len() != 0 {
				t.Fatalf("expected no output on stdout, got %q", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no output on stderr, got %q", stderr.String())
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
	cmd := &modelsCommand{}
	for _, format := range []string{"table", "json"} {
		var got string
		var err error
		if format == "json" {
			got, err = cmd.outputJSON(nil)
		} else {
			got, err = cmd.outputTable(nil)
		}
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
