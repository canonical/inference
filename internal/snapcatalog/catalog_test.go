package snapcatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publishedEntry(snap, model, repository string) string {
	return `{"snap":"` + snap + `","model_name":"` + model +
		`","full_name":"` + repository + `","html_url":"https://github.com/` + repository + `"}`
}

func publishedCatalog(entries ...string) string {
	return "[" + strings.Join(entries, ",") + "]"
}

func writeCatalogFile(t *testing.T, dir string, entries ...string) string {
	t.Helper()
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, []byte(publishedCatalog(entries...)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseEntriesSortsBySnapName(t *testing.T) {
	data := publishedCatalog(
		publishedEntry("qwen3", "Qwen 3", "canonical/qwen3-snap"),
		publishedEntry("gemma4", "Gemma 4", "canonical/gemma4-snap"),
	)

	entries, err := ParseEntries([]byte(data))
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	want := Entry{
		SnapName:      "gemma4",
		ModelName:     "Gemma 4",
		Repository:    "canonical/gemma4-snap",
		RepositoryURL: "https://github.com/canonical/gemma4-snap",
	}
	if len(entries) != 2 || entries[0] != want || entries[1].SnapName != "qwen3" {
		t.Fatalf("got %+v", entries)
	}
}

func TestParseEntriesIgnoresUnknownFields(t *testing.T) {
	data := `[{"snap":"qwen3","model_name":"Qwen 3","full_name":"canonical/qwen3-snap",
		"html_url":"https://github.com/canonical/qwen3-snap","added_at":"2026-01-01"}]`

	entries, err := ParseEntries([]byte(data))
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].SnapName != "qwen3" {
		t.Fatalf("got %+v", entries)
	}
}

func TestParseEntriesAllowsEmptyCatalog(t *testing.T) {
	entries, err := ParseEntries([]byte(`[]`))
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %+v, want no entries", entries)
	}
}

func TestParseEntriesRejectsMalformedJSON(t *testing.T) {
	for name, data := range map[string]string{
		"not json":         `not json`,
		"object not array": `{"snap":"qwen3"}`,
		"trailing data":    publishedCatalog(publishedEntry("qwen3", "Qwen 3", "canonical/qwen3-snap")) + " extra",
	} {
		if _, err := ParseEntries([]byte(data)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestReadUsesConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	writeCatalogFile(t, dir, publishedEntry("qwen3", "Qwen 3", "canonical/qwen3-snap"))

	entries, err := Reader{Path: filepath.Join(dir, Filename)}.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].SnapName != "qwen3" {
		t.Fatalf("got %+v", entries)
	}
}

func TestReadFailsWhenCatalogIsMissing(t *testing.T) {
	reader := Reader{Path: filepath.Join(t.TempDir(), Filename)}
	if _, err := reader.Read(); err == nil {
		t.Fatal("expected an error when the catalog file does not exist")
	}
}

func TestReadFailsWithoutConfiguredPath(t *testing.T) {
	_, err := (Reader{}).Read()
	if err == nil {
		t.Fatal("expected an error for an unconfigured reader")
	}
	if !strings.Contains(err.Error(), EnvVar) {
		t.Fatalf("got %q, want the error to name %s", err, EnvVar)
	}
}

func TestReadFailsForMalformedCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := (Reader{Path: path}).Read(); err == nil {
		t.Fatal("expected error for a malformed catalog")
	}
}

func TestDefaultPathPrefersEnvVarOverSnapCommon(t *testing.T) {
	t.Setenv(EnvVar, "/home/user/catalog.json")
	t.Setenv("SNAP_COMMON", "/var/snap/inference/common")

	if got := DefaultPath(); got != "/home/user/catalog.json" {
		t.Fatalf("DefaultPath()=%q", got)
	}
}

func TestDefaultPathFallsBackToSnapCommon(t *testing.T) {
	t.Setenv(EnvVar, "")
	t.Setenv("SNAP_COMMON", "/var/snap/inference/common")

	want := filepath.Join("/var/snap/inference/common", Filename)
	if got := DefaultPath(); got != want {
		t.Fatalf("DefaultPath()=%q, want %q", got, want)
	}
}

func TestDefaultPathIsEmptyWhenEnvironmentIsUnset(t *testing.T) {
	t.Setenv(EnvVar, "")
	t.Setenv("SNAP_COMMON", "")

	if got := DefaultPath(); got != "" {
		t.Fatalf("DefaultPath()=%q, want an empty path", got)
	}
}

func TestNewReaderUsesDefaultPath(t *testing.T) {
	t.Setenv(EnvVar, "/home/user/catalog.json")

	if reader := NewReader(); reader.Path != "/home/user/catalog.json" {
		t.Fatalf("Path=%q", reader.Path)
	}
}
