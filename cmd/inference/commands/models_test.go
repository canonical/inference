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
		{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"},
		{PublicID: "gemma4/gemma4-e4b", ProviderName: "gemma4", NativeID: "gemma4-e4b"},
		{PublicID: "openai/gpt4.5", ProviderName: "openai", NativeID: "gpt4.5"},
	}
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "table",
			format: "table",
			want: "PROVIDER  MODEL\n" +
				"gemma4    gemma4-e2b\n" +
				"          gemma4-e4b\n" +
				"openai    gpt4.5\n",
		},
		{
			name:   "json",
			format: "json",
			want: "{\n" +
				"  \"models\": [\n" +
				"    {\n" +
				"      \"id\": \"gemma4/gemma4-e2b\",\n" +
				"      \"provider\": \"gemma4\",\n" +
				"      \"model\": \"gemma4-e2b\"\n" +
				"    },\n" +
				"    {\n" +
				"      \"id\": \"gemma4/gemma4-e4b\",\n" +
				"      \"provider\": \"gemma4\",\n" +
				"      \"model\": \"gemma4-e4b\"\n" +
				"    },\n" +
				"    {\n" +
				"      \"id\": \"openai/gpt4.5\",\n" +
				"      \"provider\": \"openai\",\n" +
				"      \"model\": \"gpt4.5\"\n" +
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

func TestModelsCommandWithoutProviders(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "table", want: "PROVIDER  MODEL\n"},
		{name: "json", args: []string{"--format=json"}, want: "{\n  \"models\": []\n}\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext()
			ctx.SnapCatalog = &snapcatalog.Reader{}
			err := execute(Models(ctx), test.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stdout.String() != test.want {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.want)
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
		want := "PROVIDER  MODEL\n"
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
		{ProviderName: "gemma4", Models: []models.Model{{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := "PROVIDER  MODEL\ngemma4    gemma4-e2b\n"; stdout.String() != want {
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
		{ProviderName: "gemma4", Models: []models.Model{{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestModelsPrintsEmptyListWithHintWhenNoModelCanBeRetrieved(t *testing.T) {
	wantStdErr := "\nHint: Unable to retrieve models from some providers. Run \"inference status\" for details.\n"
	failing := []models.ProviderModels{
		{ProviderName: "first", Models: []models.Model{}, Err: errors.New("upstream returned 500")},
		{ProviderName: "second", Models: []models.Model{}, Err: errors.New("connection refused")},
	}

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "table", format: "table", want: "PROVIDER  MODEL\n"},
		{name: "json", format: "json", want: "{\n  \"models\": []\n}\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext()
			cmd := &modelsCommand{Context: ctx, format: test.format}
			err := cmd.printProviderModels(failing)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stdout.String() != test.want {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.want)
			}
			if stderr.String() != wantStdErr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), wantStdErr)
			}
		})
	}
}
