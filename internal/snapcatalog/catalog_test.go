package snapcatalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func publishedEntry(snap, model, repository string) string {
	return `{"snap":"` + snap + `","model_name":"` + model +
		`","repo_url":"https://github.com/` + repository + `"}`
}

func publishedEntryWithEngines(snap, engines string) string {
	return `{"snap":"` + snap + `","model_name":"Model","repo_url":"https://example.com","engines":` + engines + `}`
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

	if len(entries) != 2 || entries[0].SnapName != "gemma4" || entries[1].SnapName != "qwen3" {
		t.Fatalf("got %+v", entries)
	}
}

func TestParseEntriesEngines(t *testing.T) {
	tests := []struct {
		name    string
		engines string
		want    []Engine
	}{
		{
			name:    "sorts engines by name",
			engines: `[{"name":"nvidia-gpu","runtime":"llamacpp-cuda"},{"name":"cpu","runtime":"llamacpp"}]`,
			want:    []Engine{{Name: "cpu", Runtime: "llamacpp"}, {Name: "nvidia-gpu", Runtime: "llamacpp-cuda"}},
		},
		{
			name:    "engines are already sorted",
			engines: `[{"name":"cpu","runtime":"llamacpp"},{"name":"nvidia-gpu","runtime":"llamacpp-cuda"}]`,
			want:    []Engine{{Name: "cpu", Runtime: "llamacpp"}, {Name: "nvidia-gpu", Runtime: "llamacpp-cuda"}},
		},
		{
			name:    "engines without name are skipped",
			engines: `[{"name":"","runtime":"llamacpp"},{"name":"nvidia-gpu","runtime":"llamacpp-cuda"}]`,
			want:    []Engine{{Name: "nvidia-gpu", Runtime: "llamacpp-cuda"}},
		},
		{
			name:    "engines with duplicate names are skipped",
			engines: `[{"name":"cpu","runtime":"llamacpp"},{"name":"cpu","runtime":"llamacpp-other"}]`,
			want:    []Engine{{Name: "cpu", Runtime: "llamacpp"}},
		},
		{
			name:    "null default model decodes as empty",
			engines: `[{"name":"cpu","runtime":"llamacpp","default_model":null,"models":["smollm2-135m"]}]`,
			want:    []Engine{{Name: "cpu", Runtime: "llamacpp", Models: []string{"smollm2-135m"}}},
		},
		{
			name:    "empty engine list",
			engines: `[]`,
			want:    []Engine{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := publishedCatalog(publishedEntryWithEngines("smollm2", tt.engines))

			entries, err := ParseEntries([]byte(data))
			if err != nil {
				t.Fatalf("ParseEntries: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("got %d entries, want 1", len(entries))
			}
			if !reflect.DeepEqual(entries[0].Engines, tt.want) {
				t.Errorf("got:\n%+v\nwant:\n%+v", entries[0].Engines, tt.want)
			}
		})
	}
}

func TestParseEntriesDecodesPublishedEntry(t *testing.T) {
	data := `[{"snap":"smollm2","model_name":"SmolLM2","repo_url":"https://github.com/canonical/smollm2-snap",
		"engines":[{"name":"cpu","runtime":"llamacpp","default_model":"smollm2-135m","models":["smollm2-135m"]},
		{"name":"nvidia-gpu","runtime":"llamacpp-cuda","default_model":"smollm2-135m","models":["smollm2-135m"]}]}]`

	entries, err := ParseEntries([]byte(data))
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	want := []Entry{{
		SnapName:      "smollm2",
		ModelName:     "SmolLM2",
		RepositoryURL: "https://github.com/canonical/smollm2-snap",
		Engines: []Engine{
			{Name: "cpu", Runtime: "llamacpp", DefaultModel: "smollm2-135m", Models: []string{"smollm2-135m"}},
			{Name: "nvidia-gpu", Runtime: "llamacpp-cuda", DefaultModel: "smollm2-135m", Models: []string{"smollm2-135m"}},
		},
	}}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("got:\n%+v\nwant:\n%+v", entries, want)
	}
}

func TestParseEntriesAllowsMissingEngines(t *testing.T) {
	data := publishedCatalog(publishedEntry("smollm2", "SmolLM2", "canonical/smollm2-snap"))

	entries, err := ParseEntries([]byte(data))
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Engines != nil {
		t.Fatalf("got %+v, want one entry without engines", entries)
	}
}

func TestParseEntriesIgnoresUnknownFields(t *testing.T) {
	data := `[{"snap":"qwen3","model_name":"Qwen 3",
		"repo_url":"https://github.com/canonical/qwen3-snap","added_at":"2026-01-01"}]`

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
		"not json":          `not json`,
		"object not array":  `{"snap":"qwen3"}`,
		"trailing data":     publishedCatalog(publishedEntry("qwen3", "Qwen 3", "canonical/qwen3-snap")) + " extra",
		"engines as object": publishedCatalog(publishedEntryWithEngines("qwen3", `{}`)),
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

func TestRefreshReplacesCatalog(t *testing.T) {
	data := publishedCatalog(publishedEntry("qwen3", "Qwen 3", "canonical/qwen3-snap"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, data)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), Filename)
	if err := os.WriteFile(path, []byte("old catalog"), 0o600); err != nil {
		t.Fatal(err)
	}
	refresher := Refresher{Client: server.Client(), Path: path, URL: server.URL}
	if err := refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Fatalf("catalog=%q, want %q", got, data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode=%o, want 644", got)
	}
}

func TestRefreshPreservesCatalogOnInvalidResponse(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
	}{
		{
			name: "HTTP error",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}),
		},
		{
			name: "malformed JSON",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "not json")
			}),
		},
		{
			name: "oversized response",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(make([]byte, maxCatalogSizeBytes+1))
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			path := filepath.Join(t.TempDir(), Filename)
			const existing = `[{"snap":"existing"}]`
			if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
				t.Fatal(err)
			}

			err := (Refresher{Client: server.Client(), Path: path, URL: server.URL}).Refresh(context.Background())
			if err == nil {
				t.Fatal("expected refresh error")
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != existing {
				t.Fatalf("catalog=%q, want existing catalog", got)
			}
		})
	}
}

func TestRefreshHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	path := filepath.Join(t.TempDir(), Filename)
	go func() {
		done <- (Refresher{
			Client: server.Client(),
			Path:   path,
			URL:    server.URL,
		}).Refresh(ctx)
	}()
	<-started
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Refresh error=%v, want context canceled", err)
	}
}

func TestRefreshFailsWithoutConfiguredPath(t *testing.T) {
	err := (Refresher{URL: URL}).Refresh(context.Background())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Refresh error=%v, want ErrNotConfigured", err)
	}
}

func TestNewRefresherUsesDefaults(t *testing.T) {
	t.Setenv(EnvVar, "/home/user/catalog.json")
	client := &http.Client{}
	refresher := NewRefresher(client)
	if refresher.Client != client || refresher.Path != "/home/user/catalog.json" || refresher.URL != URL {
		t.Fatalf("refresher=%+v", refresher)
	}
}
