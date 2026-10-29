package providers

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func TestProviderInstalled(t *testing.T) {
	tests := []struct {
		name string
		p    Provider
		want bool
	}{
		{name: "unknown snap", p: Provider{Backend: BackendSnap, State: StateUnknown}, want: false},
		{name: "not installed snap", p: Provider{Backend: BackendSnap, State: StateNotInstalled}, want: false},
		{name: "disabled snap", p: Provider{Backend: BackendSnap, State: StateDisabled}, want: true},
		{name: "enabled snap", p: Provider{Backend: BackendSnap, State: StateEnabled}, want: true},
		{name: "configured OpenAI provider", p: Provider{Backend: BackendProxy}, want: true},
		{name: "unknown provider backend", p: Provider{Backend: ProviderBackend("unknown")}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Installed(); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestList(t *testing.T) {
	cfg := &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	catalog := snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","repo_url":"https://example.com/gemma4"},
		{"snap":"qwen3","model_name":"Qwen 3","repo_url":"https://example.com/qwen3"},
		{"snap":"smollm2","model_name":"SmolLM2","repo_url":"https://example.com/smollm2"}
	]`)
	client, _ := snapd.NewFakeServer(t, map[string]string{
		"gemma4": snapd.SnapStatusActive,
		"qwen3":  snapd.SnapStatusInstalled,
	})

	t.Run("all providers", func(t *testing.T) {
		got, err := ListAll(context.Background(), cfg, catalog, client, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		want := []Provider{
			{Name: "gemma4", Backend: BackendSnap, State: StateEnabled, Connection: ConnectionNotConnected},
			{Name: "qwen3", Backend: BackendSnap, State: StateDisabled, Connection: ConnectionNotConnected},
			{Name: "smollm2", Backend: BackendSnap, State: StateNotInstalled, Connection: ConnectionNotConnected},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d providers, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("provider %d: got %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("installed only", func(t *testing.T) {
		got, err := ListInstalled(context.Background(), cfg, catalog, client, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		want := []Provider{
			{Name: "gemma4", Backend: BackendSnap, State: StateEnabled, Connection: ConnectionNotConnected},
			{Name: "qwen3", Backend: BackendSnap, State: StateDisabled, Connection: ConnectionNotConnected},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d providers, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("provider %d: got %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("searching one provider doesn't query others", func(t *testing.T) {
		client, requests := snapd.NewFakeServer(t, map[string]string{
			"gemma4": snapd.SnapStatusActive,
			"qwen3":  snapd.SnapStatusInstalled,
		})

		got, err := Find(context.Background(), cfg, catalog, client, "", "gemma4")
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		want := Provider{
			Name:       "gemma4",
			Backend:    BackendSnap,
			State:      StateEnabled,
			Connection: ConnectionNotConnected,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("provider: got %+v, want %+v", got, want)
		}

		// filtering happens before the snapd lookup
		if n := requests.Load(); n != 1 {
			t.Fatalf("expected exactly 1 snapd request, got %d", n)
		}
	})
}

func TestListReadsProvidersFromConfigFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	configJSON := `{
		"version": 1,
		"discover-providers": false,
		"providers": {
			"local": {
				"enabled": true,
				"backend": "snap",
				"base-url": "http://localhost:8326/v1",
				"snap": "gemma4"
			},
			"openai": {
				"enabled": true,
				"backend": "proxy",
				"base-url": "https://api.openai.com/v1",
				"api": "openai",
				"key-file": "/var/snap/inference/common/.keys/openai"
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	providerConfig := &config.ConfigReader{Path: configPath}
	catalog := snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"local","model_name":"Local","repo_url":"https://example.com/local",
		 "engines":[{"name":"cpu","runtime":"llamacpp","default_model":"gemma4","models":["gemma4"]}]}
	]`)
	client, _ := snapd.NewFakeServer(t, map[string]string{
		"local": snapd.SnapStatusActive,
	})

	got, err := ListAll(context.Background(), providerConfig, catalog, client, "")
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	want := []Provider{
		{
			Name:       "openai",
			Backend:    BackendProxy,
			State:      StateEnabled,
			Connection: ConnectionNotConnected,
			BaseURL:    "https://api.openai.com/v1",
			Engines:    []Engine{},
		},
		{
			Name:       "local",
			Backend:    BackendSnap,
			State:      StateEnabled,
			Connection: ConnectionNotConnected,
			BaseURL:    "http://localhost:8326/v1",
			Engines: []Engine{
				{Name: "cpu", Runtime: "llamacpp", DefaultModel: "gemma4", Models: []string{"gemma4"}},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFind(t *testing.T) {
	cfg := &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	catalog := snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","repo_url":"https://example.com/gemma4"},
		{"snap":"qwen3","model_name":"Qwen 3","repo_url":"https://example.com/qwen3"}
	]`)
	client, requests := snapd.NewFakeServer(t, map[string]string{
		"gemma4": snapd.SnapStatusActive,
		"qwen3":  snapd.SnapStatusInstalled,
	})

	t.Run("matching provider", func(t *testing.T) {
		got, err := Find(context.Background(), cfg, catalog, client, "", "gemma4")
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		want := Provider{Name: "gemma4", Backend: BackendSnap, State: StateEnabled, Connection: ConnectionNotConnected}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}

		// filtering happens before the snapd lookup
		if n := requests.Load(); n != 1 {
			t.Fatalf("expected exactly 1 snapd request, got %d", n)
		}
	})

	t.Run("nothing matches", func(t *testing.T) {
		_, err := Find(context.Background(), cfg, catalog, client, "", "oopsie-daisy")
		if err == nil {
			t.Fatal("expected an error for an unknown provider")
		}
		if !strings.Contains(err.Error(), "oopsie-daisy") {
			t.Fatalf("expected error to mention the requested name, got: %v", err)
		}
	})

	t.Run("empty provider", func(t *testing.T) {
		_, err := Find(context.Background(), cfg, catalog, client, "", "")
		if err == nil {
			t.Fatal("expected an error for an empty provider")
		}
	})
}

func TestListReturnsMalformedConfigError(t *testing.T) {
	cfg := &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	if err := os.WriteFile(cfg.Path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := snapcatalog.WriteFakeCatalog(t, `[]`)
	client, _ := snapd.NewFakeServer(t, nil)

	_, err := ListAll(context.Background(), cfg, catalog, client, "")
	if err == nil || !strings.Contains(err.Error(), "reading configured providers") {
		t.Fatalf("got error %v, want configured providers error", err)
	}
}

func TestFindReturnsCatalogEngines(t *testing.T) {
	cfg := &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	catalog := snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","repo_url":"https://example.com/gemma4",
		 "engines":[{"name":"cpu","runtime":"llamacpp","default_model":"gemma4-e4b","models":["gemma4-e2b","gemma4-e4b"]}]},
		{"snap":"smollm2","model_name":"SmolLM2","repo_url":"https://example.com/smollm2",
		 "engines":[{"name":"cpu","runtime":"llamacpp","default_model":"smollm2-135m","models":["smollm2-135m"]}]}
	]`)
	root := t.TempDir()
	writeProviderEnv(t, root, "smollm2-mount", "OPENAI_BASE_URL=http://localhost:8080/v1\nSNAP_NAME=smollm2\n")
	writeProviderEnv(t, root, "custom-mount", "OPENAI_BASE_URL=http://localhost:8081/v1\nSNAP_NAME=custom\n")
	client, _ := snapd.NewFakeServer(t, map[string]string{
		"smollm2": snapd.SnapStatusActive,
		"custom":  snapd.SnapStatusActive,
	})

	t.Run("catalog snap that is not installed", func(t *testing.T) {
		got, err := Find(context.Background(), cfg, catalog, client, root, "gemma4")
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		want := []Engine{
			{Name: "cpu", Runtime: "llamacpp", DefaultModel: "gemma4-e4b", Models: []string{"gemma4-e2b", "gemma4-e4b"}},
		}
		if !reflect.DeepEqual(got.Engines, want) {
			t.Fatalf("got %+v, want %+v", got.Engines, want)
		}
	})

	t.Run("catalog snap that is also connected", func(t *testing.T) {
		got, err := Find(context.Background(), cfg, catalog, client, root, "smollm2")
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		want := []Engine{
			{Name: "cpu", Runtime: "llamacpp", DefaultModel: "smollm2-135m", Models: []string{"smollm2-135m"}},
		}
		if !reflect.DeepEqual(got.Engines, want) {
			t.Fatalf("got %+v, want %+v", got.Engines, want)
		}
		if got.BaseURL != "http://localhost:8080/v1" {
			t.Fatalf("got BaseURL %q, want %q", got.BaseURL, "http://localhost:8080/v1")
		}
	})

	t.Run("connected snap missing from the catalog", func(t *testing.T) {
		got, err := Find(context.Background(), cfg, catalog, client, root, "custom")
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if got.Engines != nil {
			t.Fatalf("got %+v, want no engines", got.Engines)
		}
	})
}

func TestListTreatsMissingCatalogAsEmpty(t *testing.T) {
	cfg := &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	client, _ := snapd.NewFakeServer(t, nil)

	for _, catalog := range []*snapcatalog.Reader{
		{},
		{Path: filepath.Join(t.TempDir(), "missing.json")},
	} {
		got, err := ListAll(context.Background(), cfg, catalog, client, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no providers", got)
		}
	}
}

func TestListReturnsMalformedCatalogError(t *testing.T) {
	cfg := &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	catalog := snapcatalog.WriteFakeCatalog(t, "not json")
	client, _ := snapd.NewFakeServer(t, nil)

	if _, err := ListAll(context.Background(), cfg, catalog, client, ""); err == nil {
		t.Fatal("expected malformed catalog error")
	}
}

func TestSnapStatusToProviderState(t *testing.T) {
	tests := []struct {
		name       string
		snapStatus string
		want       LifecycleState
		wantErr    bool
	}{
		{
			name:       "not installed",
			snapStatus: snapd.StatusNotInstalled,
			want:       StateNotInstalled,
		},
		{
			name:       "active",
			snapStatus: snapd.SnapStatusActive,
			want:       StateEnabled,
		},
		{
			name:       "installed",
			snapStatus: snapd.SnapStatusInstalled,
			want:       StateDisabled,
		},
		{
			name:       "unexpected status",
			snapStatus: "bogus",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := snapStatusToProviderState(tt.snapStatus)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
