package commands

import (
	"errors"
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

func TestModelsCommandErrorsWithoutProviders(t *testing.T) {
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
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
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

func TestModelsPrintsHintWhenSomeProvidersFail(t *testing.T) {
	ctx, stdout, stderr := newTestContext()
	cmd := &modelsCommand{Context: ctx, format: "table"}
	err := cmd.printProviderModels([]models.ProviderModels{
		{ProviderName: "broken", Models: []models.Model{}, Err: errors.New("upstream returned 500")},
		{ProviderName: "gemma4", Models: []models.Model{{PublicID: "gemma4/gemma4-e2b"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := "ID\ngemma4/gemma4-e2b\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	wantHint := "\nHint: Unable to retrieve models from some providers. Run \"inference status\" for details.\n"
	if stderr.String() != wantHint {
		t.Fatalf("stderr = %q, want %q", stderr.String(), wantHint)
	}
}

func TestModelsOmitsHintWhenAllProvidersSucceed(t *testing.T) {
	ctx, _, stderr := newTestContext()
	cmd := &modelsCommand{Context: ctx, format: "table"}
	err := cmd.printProviderModels([]models.ProviderModels{
		{ProviderName: "gemma4", Models: []models.Model{{PublicID: "gemma4/gemma4-e2b"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestModelsFailsWhenNoModelCanBeRetrieved(t *testing.T) {
	wantErr := "unable to retrieve a model from the providers\n\nHint: Run \"inference status\" for details"
	failing := []models.ProviderModels{
		{ProviderName: "first", Models: []models.Model{}, Err: errors.New("upstream returned 500")},
		{ProviderName: "second", Models: []models.Model{}, Err: errors.New("connection refused")},
	}

	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext()
			cmd := &modelsCommand{Context: ctx, format: format}
			err := cmd.printProviderModels(failing)
			if err == nil || err.Error() != wantErr {
				t.Fatalf("error = %v, want %q", err, wantErr)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected stdout: %q", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}
