package commands

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/models"
	"github.com/canonical/inference/internal/snapcatalog"
)

// Command level tests
func TestModelsCommandWithoutProviders(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStdout string
		wantStderr string
	}{
		{name: "table", wantStdout: "", wantStderr: "No models found.\n"},
		{name: "json", args: []string{"--format=json"}, wantStdout: "{\n  \"models\": []\n}\n", wantStderr: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext()
			ctx.SnapCatalog = &snapcatalog.Reader{}
			err := execute(Models(ctx), test.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stdout.String() != test.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.wantStdout)
			}
			if stderr.String() != test.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantStderr)
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

//
// Standard stream testing
//

func Example_modelsCommand_printModelsTable() {
	cmd := &modelsCommand{Context: &common.Context{Stdout: os.Stdout, Stderr: os.Stderr}}
	list := []models.Model{
		{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"},
		{PublicID: "openai/gpt4.5", ProviderName: "openai", NativeID: "gpt4.5"},
	}
	if err := cmd.printModelsTable(list); err != nil {
		panic(err)
	}

	// Output:
	// PROVIDER  MODEL
	// gemma4    gemma4-e2b
	// openai    gpt4.5
}

func TestPrintModelsTable_Empty(t *testing.T) {
	ctx, stdout, stderr := newTestContext()
	cmd := &modelsCommand{Context: ctx}
	if err := cmd.printModelsTable(nil); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if want := "No models found.\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func Example_modelsCommand_printModelsJSON() {
	cmd := &modelsCommand{Context: &common.Context{Stdout: os.Stdout, Stderr: os.Stderr}}
	list := []models.Model{
		{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"},
	}
	if err := cmd.printModelsJSON(list); err != nil {
		panic(err)
	}

	// Output:
	// {
	//   "models": [
	//     {
	//       "id": "gemma4/gemma4-e2b",
	//       "provider": "gemma4",
	//       "model": "gemma4-e2b"
	//     }
	//   ]
	// }
}

func Example_modelsCommand_printModelsJSON_empty() {
	cmd := &modelsCommand{Context: &common.Context{Stdout: os.Stdout, Stderr: os.Stderr}}
	if err := cmd.printModelsJSON(nil); err != nil {
		panic(err)
	}

	// Output:
	// {
	//   "models": []
	// }
}

//
// Other unit tests
//

func TestOutputTable(t *testing.T) {
	list := []models.Model{
		{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"},
		{PublicID: "gemma4/gemma4-e4b", ProviderName: "gemma4", NativeID: "gemma4-e4b"},
		{PublicID: "openai/gpt4.5", ProviderName: "openai", NativeID: "gpt4.5"},
	}
	want := `PROVIDER  MODEL
gemma4    gemma4-e2b
          gemma4-e4b
openai    gpt4.5
`

	cmd := &modelsCommand{}
	got, err := cmd.outputTable(list)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestOutputJSON(t *testing.T) {
	list := []models.Model{
		{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"},
		{PublicID: "gemma4/gemma4-e4b", ProviderName: "gemma4", NativeID: "gemma4-e4b"},
		{PublicID: "openai/gpt4.5", ProviderName: "openai", NativeID: "gpt4.5"},
	}
	want := `{
  "models": [
    {
      "id": "gemma4/gemma4-e2b",
      "provider": "gemma4",
      "model": "gemma4-e2b"
    },
    {
      "id": "gemma4/gemma4-e4b",
      "provider": "gemma4",
      "model": "gemma4-e4b"
    },
    {
      "id": "openai/gpt4.5",
      "provider": "openai",
      "model": "gpt4.5"
    }
  ]
}
`

	cmd := &modelsCommand{}
	got, err := cmd.outputJSON(list)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
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

func TestFilterAvailableModels_SomeProvidersFail(t *testing.T) {
	cmd := &modelsCommand{}
	gemma := models.Model{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"}
	got, err := cmd.filterAvailableModels([]models.ProviderModels{
		{ProviderName: "broken", Models: []models.Model{}, Err: errors.New("upstream returned 500")},
		{ProviderName: "gemma4", Models: []models.Model{gemma}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []models.Model{gemma}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFilterAvailableModels_AllProvidersSucceed(t *testing.T) {
	cmd := &modelsCommand{}
	gemma := models.Model{PublicID: "gemma4/gemma4-e2b", ProviderName: "gemma4", NativeID: "gemma4-e2b"}
	openai := models.Model{PublicID: "openai/gpt4.5", ProviderName: "openai", NativeID: "gpt4.5"}
	got, err := cmd.filterAvailableModels([]models.ProviderModels{
		{ProviderName: "gemma4", Models: []models.Model{gemma}},
		{ProviderName: "openai", Models: []models.Model{openai}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []models.Model{gemma, openai}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFilterAvailableModels_AllProvidersFail(t *testing.T) {
	cmd := &modelsCommand{}
	got, err := cmd.filterAvailableModels([]models.ProviderModels{
		{ProviderName: "first", Models: []models.Model{}, Err: errors.New("upstream returned 500")},
		{ProviderName: "second", Models: []models.Model{}, Err: errors.New("connection refused")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no models", got)
	}
}
