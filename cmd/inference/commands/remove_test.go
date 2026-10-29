package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/spf13/cobra"
)

func TestRemove_Completion(t *testing.T) {
	for _, test := range []struct {
		name      string
		args      []string
		prefix    string
		catalog   string
		want      []string
		wantError bool
	}{
		{
			name:    "providers and snaps",
			catalog: `[{"snap":"smollm2"},{"snap":"shared"}]`,
			want:    []string{"custom", "disabled", "shared", "smollm2", "snap-configured"},
		},
		{
			name:    "configured and catalog snaps without overlaps",
			catalog: `[{"snap":"snap-catalog"},{"snap":"smollm2"}]`,
			want:    []string{"custom", "disabled", "shared", "smollm2", "snap-catalog", "snap-configured"},
		},
		{
			name:    "configured and catalog snaps with multiple overlaps",
			catalog: `[{"snap":"snap-configured"},{"snap":"smollm2"},{"snap":"shared"}]`,
			want:    []string{"custom", "disabled", "shared", "smollm2", "snap-configured"},
		},
		{
			name:    "snap prefix across config and catalog with overlap",
			prefix:  "snap-",
			catalog: `[{"snap":"snap-configured"},{"snap":"snap-catalog"},{"snap":"shared"}]`,
			want:    []string{"snap-catalog", "snap-configured"},
		},
		{
			name:    "provider prefix",
			prefix:  "cus",
			catalog: `[{"snap":"smollm2"}]`,
			want:    []string{"custom"},
		},
		{
			name:    "snap prefix",
			prefix:  "smo",
			catalog: `[{"snap":"smollm2"}]`,
			want:    []string{"smollm2"},
		},
		{
			name:    "no matches",
			prefix:  "missing",
			catalog: `[]`,
		},
		{
			name:    "empty catalog",
			catalog: `[]`,
			want:    []string{"custom", "disabled", "shared", "snap-configured"},
		},
		{
			name:      "invalid catalog",
			catalog:   "not json",
			wantError: true,
		},
		{
			name:      "missing catalog",
			wantError: true,
		},
		{
			name: "argument already supplied",
			args: []string{"custom"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), snapcatalog.Filename)
			t.Setenv(snapcatalog.EnvVar, path)
			if test.catalog != "" {
				if err := os.WriteFile(path, []byte(test.catalog), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, _, _ := newTestContext()
			ctx.Config.Providers["custom"] = config.Provider{Backend: config.BackendProxy, Enabled: true}
			ctx.Config.Providers["disabled"] = config.Provider{Backend: config.BackendProxy}
			ctx.Config.Providers["shared"] = config.Provider{Backend: config.BackendSnap, Enabled: true, Snap: new("shared")}
			ctx.Config.Providers["snap-configured"] = config.Provider{Backend: config.BackendSnap, Enabled: true, Snap: new("snap-configured")}
			cmd := Remove(ctx)

			got, directive := cmd.ValidArgsFunction(cmd, test.args, test.prefix)
			wantDirective := cobra.ShellCompDirectiveNoFileComp
			if test.wantError {
				wantDirective = cobra.ShellCompDirectiveError
			}
			if directive != wantDirective {
				t.Fatalf("got directive %v, want %v", directive, wantDirective)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("got completions %v, want %v", got, test.want)
			}
		})
	}
}

func TestRemove_CustomProvider(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		withKey bool
	}{
		{name: "without key", enabled: true},
		{name: "with key", enabled: true, withKey: true},
		{name: "disabled", withKey: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SNAP_DATA", t.TempDir())
			ctx, _, _ := newTestContext()
			cfg := config.NewConfig()
			cfg.DiscoverProviders = false
			cfg.Providers["custom"] = config.Provider{
				Backend: config.BackendProxy,
				Enabled: test.enabled,
				BaseURL: "https://example.com/v1",
			}
			cfg.Providers["keep"] = config.Provider{
				Backend: config.BackendProxy,
				Enabled: true,
				BaseURL: "https://keep.example.com/v1",
			}
			var keyFile string
			if test.withKey {
				key := "test-api-key"
				if err := cfg.SetProviderKey("custom", &key); err != nil {
					t.Fatalf("SetProviderKey: %v", err)
				}
				keyFile = *cfg.Providers["custom"].KeyFile
			}
			if err := config.Write(config.ConfigFilePath, &cfg); err != nil {
				t.Fatalf("Write: %v", err)
			}
			ctx.Config = &cfg

			if err := execute(Remove(ctx), "custom"); err != nil {
				t.Fatalf("Remove: %v", err)
			}

			got, err := config.Read(config.ConfigFilePath)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			delete(cfg.Providers, "custom")
			if !reflect.DeepEqual(got, &cfg) {
				t.Fatalf("got config %+v, want %+v", got, &cfg)
			}
			if test.withKey {
				if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
					t.Fatalf("expected key file to be removed, got %v", err)
				}
			}
		})
	}
}

func TestRemove_ConfiguredProviderFailurePreservesConfig(t *testing.T) {
	for _, test := range []struct {
		name      string
		backend   config.ProviderBackend
		withKey   bool
		wantError string
	}{
		{
			name:      "non-proxy provider",
			backend:   config.BackendSnap,
			wantError: "removing non-proxy providers from config is not supported",
		},
		{
			name:      "key removal fails",
			backend:   config.BackendProxy,
			withKey:   true,
			wantError: "removing provider: deleting key file:",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapData := t.TempDir()
			t.Setenv("SNAP_DATA", snapData)
			ctx, _, _ := newTestContext()
			cfg := config.NewConfig()
			provider := config.Provider{Backend: test.backend, Enabled: true}
			if test.withKey {
				keyFile := filepath.Join(snapData, "missing.key")
				provider.KeyFile = &keyFile
			}
			cfg.Providers["custom"] = provider
			if err := config.Write(config.ConfigFilePath, &cfg); err != nil {
				t.Fatalf("Write: %v", err)
			}
			ctx.Config = &cfg

			err := execute(Remove(ctx), "custom")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got error %v, want %q", err, test.wantError)
			}
			got, err := config.Read(config.ConfigFilePath)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !reflect.DeepEqual(got, &cfg) {
				t.Fatalf("config changed after failed removal: got %+v, want %+v", got, &cfg)
			}
		})
	}
}

func TestRemove_UsesLoadedConfig(t *testing.T) {
	snapData := t.TempDir()
	t.Setenv("SNAP_DATA", snapData)
	ctx, _, _ := newTestContext()
	ctx.Config.Providers["custom"] = config.Provider{Backend: config.BackendProxy}
	if err := os.WriteFile(filepath.Join(snapData, "config.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := execute(Remove(ctx), "custom"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err := config.Read(config.ConfigFilePath)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, exists := got.Providers["custom"]; exists {
		t.Fatal("custom provider remains in persisted config")
	}
	if !reflect.DeepEqual(got, ctx.Config) {
		t.Fatalf("got config %+v, want %+v", got, ctx.Config)
	}
}

func TestRemove_ConfigWriteFailure(t *testing.T) {
	t.Setenv("SNAP_DATA", filepath.Join(t.TempDir(), "missing"))
	cfg := config.NewConfig()
	provider := config.Provider{Backend: config.BackendProxy}
	cfg.Providers["custom"] = provider
	ctx, _, _ := newTestContext()
	ctx.Config = &cfg
	cmd := &removeCommand{ctx: ctx}

	err := cmd.removeConfiguredProvider(provider, "custom")
	if err == nil || !strings.Contains(err.Error(), "writing config:") {
		t.Fatalf("expected config write error, got %v", err)
	}
}
