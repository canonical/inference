package snapcatalog

import (
	"strings"
	"testing"
)

const sampleCatalog = `<!doctype html>
<html><body>
<table>
  <thead><tr><th>Snap</th><th>Model</th><th>Repository</th></tr></thead>
  <tbody>
    <tr>
      <td>glm-4-7-flash</td>
      <td>GLM 4.7 Flash</td>
      <td><a href="https://github.com/canonical/glm-4.7-flash-snap">canonical/glm-4.7-flash-snap</a></td>
    </tr>
    <tr>
      <td>gemma4</td>
      <td>Gemma 4</td>
      <td><a href="https://github.com/canonical/gemma4-snap">canonical/gemma4-snap</a></td>
    </tr>
  </tbody>
</table>
</body></html>`

func TestParse(t *testing.T) {
	entries, err := Parse([]byte(sampleCatalog))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %+v", entries)
	}
	first := entries[1]
	if first.SnapName != "glm-4-7-flash" ||
		first.Model != "GLM 4.7 Flash" ||
		first.Repository != "canonical/glm-4.7-flash-snap" {
		t.Fatalf("got %+v", first)
	}
}

func TestParseRejectsMissingTable(t *testing.T) {
	if _, err := Parse([]byte(`<html><body>No catalog</body></html>`)); err == nil {
		t.Fatal("expected error for missing table")
	}
}

func TestParseRejectsChangedHeaders(t *testing.T) {
	data := strings.Replace(sampleCatalog, "<th>Snap</th>", "<th>Name</th>", 1)
	if _, err := Parse([]byte(data)); err == nil {
		t.Fatal("expected error for changed headers")
	}
}

func TestParseRejectsEmptyCatalog(t *testing.T) {
	data := `<table><tr><th>Snap</th><th>Model</th><th>Repository</th></tr></table>`
	if _, err := Parse([]byte(data)); err == nil {
		t.Fatal("expected error for empty catalog")
	}
}

func TestParseRejectsInvalidRows(t *testing.T) {
	tests := map[string]string{
		"missing cell": `<tr><td>gemma4</td><td>Gemma 4</td></tr>`,
		"invalid name": catalogRow("Gemma_4", "Gemma 4", "canonical/gemma4-snap", "https://github.com/canonical/gemma4-snap"),
		"empty model":  catalogRow("gemma4", "", "canonical/gemma4-snap", "https://github.com/canonical/gemma4-snap"),
		"wrong host":   catalogRow("gemma4", "Gemma 4", "canonical/gemma4-snap", "https://example.com/canonical/gemma4-snap"),
		"text mismatch": catalogRow(
			"gemma4", "Gemma 4", "canonical/other-snap", "https://github.com/canonical/gemma4-snap",
		),
	}
	for name, row := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(catalogWithRows(row))); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseRejectsDuplicates(t *testing.T) {
	tests := map[string]string{
		"name": catalogRow("gemma4", "Other", "canonical/other-snap", "https://github.com/canonical/other-snap"),
		"repository": catalogRow(
			"other", "Other", "canonical/gemma4-snap", "https://github.com/canonical/gemma4-snap",
		),
	}
	base := catalogRow("gemma4", "Gemma 4", "canonical/gemma4-snap", "https://github.com/canonical/gemma4-snap")
	for name, duplicate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(catalogWithRows(base + duplicate))); err == nil {
				t.Fatal("expected duplicate error")
			}
		})
	}
}

func TestValidateSnapName(t *testing.T) {
	valid := []string{"gemma4", "glm-4-7-flash", "nomic-embed-text-v1-5", "qwen3-8"}
	for _, name := range valid {
		if err := ValidateSnapName(name); err != nil {
			t.Errorf("expected %q to be valid, got %v", name, err)
		}
	}

	invalid := []string{"", "Gemma4", "gemma_4", "-gemma4", "gemma4-", "gemma--4"}
	for _, name := range invalid {
		if err := ValidateSnapName(name); err == nil {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}

func catalogWithRows(rows string) string {
	return `<table><thead><tr><th>Snap</th><th>Model</th><th>Repository</th></tr></thead><tbody>` +
		rows + `</tbody></table>`
}

func catalogRow(name, model, repository, href string) string {
	return `<tr><td>` + name + `</td><td>` + model + `</td><td><a href="` +
		href + `">` + repository + `</a></td></tr>`
}
