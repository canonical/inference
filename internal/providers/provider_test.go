package providers

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapcatalog/snapcatalogtest"
	"github.com/canonical/inference/internal/snapd"
	"github.com/canonical/inference/internal/snapd/snapdtest"
)

func TestProviderInstalled(t *testing.T) {
	tests := []struct {
		name string
		p    Provider
		want bool
	}{
		{name: "unknown snap", p: Provider{Type: TypeInferenceSnap, State: StateUnknown}, want: false},
		{name: "not installed snap", p: Provider{Type: TypeInferenceSnap, State: StateNotInstalled}, want: false},
		{name: "disabled snap", p: Provider{Type: TypeInferenceSnap, State: StateDisabled}, want: true},
		{name: "enabled snap", p: Provider{Type: TypeInferenceSnap, State: StateEnabled}, want: true},
		{name: "configured OpenAI provider", p: Provider{Type: TypeOpenAI}, want: true},
		{name: "unknown provider type", p: Provider{Type: ProviderType("unknown")}, want: false},
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
	catalog := snapcatalogtest.WriteCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","full_name":"canonical/gemma4","html_url":"https://example.com/gemma4"},
		{"snap":"qwen3","model_name":"Qwen 3","full_name":"canonical/qwen3","html_url":"https://example.com/qwen3"},
		{"snap":"smollm2","model_name":"SmolLM2","full_name":"canonical/smollm2","html_url":"https://example.com/smollm2"}
	]`)
	client, _ := snapdtest.NewFakeServer(t, map[string]string{
		"gemma4": snapd.SnapStatusActive,
		"qwen3":  snapd.SnapStatusInstalled,
	})

	t.Run("all providers", func(t *testing.T) {
		got, err := ListAll(context.Background(), catalog, client, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		want := []Provider{
			{Name: "gemma4", Type: TypeInferenceSnap, State: StateEnabled, Connection: ConnectionNotConnected},
			{Name: "qwen3", Type: TypeInferenceSnap, State: StateDisabled, Connection: ConnectionNotConnected},
			{Name: "smollm2", Type: TypeInferenceSnap, State: StateNotInstalled, Connection: ConnectionNotConnected},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d providers, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("provider %d: got %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("installed only", func(t *testing.T) {
		got, err := ListInstalled(context.Background(), catalog, client, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		want := []Provider{
			{Name: "gemma4", Type: TypeInferenceSnap, State: StateEnabled, Connection: ConnectionNotConnected},
			{Name: "qwen3", Type: TypeInferenceSnap, State: StateDisabled, Connection: ConnectionNotConnected},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d providers, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("provider %d: got %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("searching one provider doesn't query others", func(t *testing.T) {
		client, requests := snapdtest.NewFakeServer(t, map[string]string{
			"gemma4": snapd.SnapStatusActive,
			"qwen3":  snapd.SnapStatusInstalled,
		})

		got, err := Find(context.Background(), catalog, client, "", "gemma4")
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		want := Provider{
			Name:       "gemma4",
			Type:       TypeInferenceSnap,
			State:      StateEnabled,
			Connection: ConnectionNotConnected,
		}
		if got != want {
			t.Fatalf("provider: got %+v, want %+v", got, want)
		}

		// filtering happens before the snapd lookup
		if n := requests.Load(); n != 1 {
			t.Fatalf("expected exactly 1 snapd request, got %d", n)
		}
	})
}

func TestFind(t *testing.T) {
	catalog := snapcatalogtest.WriteCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","full_name":"canonical/gemma4","html_url":"https://example.com/gemma4"},
		{"snap":"qwen3","model_name":"Qwen 3","full_name":"canonical/qwen3","html_url":"https://example.com/qwen3"}
	]`)
	client, requests := snapdtest.NewFakeServer(t, map[string]string{
		"gemma4": snapd.SnapStatusActive,
		"qwen3":  snapd.SnapStatusInstalled,
	})

	t.Run("matching provider", func(t *testing.T) {
		got, err := Find(context.Background(), catalog, client, "", "gemma4")
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		want := Provider{Name: "gemma4", Type: TypeInferenceSnap, State: StateEnabled, Connection: ConnectionNotConnected}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}

		// filtering happens before the snapd lookup
		if n := requests.Load(); n != 1 {
			t.Fatalf("expected exactly 1 snapd request, got %d", n)
		}
	})

	t.Run("nothing matches", func(t *testing.T) {
		_, err := Find(context.Background(), catalog, client, "", "oopsie-daisy")
		if err == nil {
			t.Fatal("expected an error for an unknown provider")
		}
		if !strings.Contains(err.Error(), "oopsie-daisy") {
			t.Fatalf("expected error to mention the requested name, got: %v", err)
		}
	})

	t.Run("empty provider", func(t *testing.T) {
		_, err := Find(context.Background(), catalog, client, "", "")
		if err == nil {
			t.Fatal("expected an error for an empty provider")
		}
	})
}

func TestListTreatsMissingCatalogAsEmpty(t *testing.T) {
	client, _ := snapdtest.NewFakeServer(t, nil)

	for _, catalog := range []*snapcatalog.Reader{
		{},
		{Path: filepath.Join(t.TempDir(), "missing.json")},
	} {
		got, err := ListAll(context.Background(), catalog, client, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no providers", got)
		}
	}
}

func TestListReturnsMalformedCatalogError(t *testing.T) {
	catalog := snapcatalogtest.WriteCatalog(t, "not json")
	client, _ := snapdtest.NewFakeServer(t, nil)

	if _, err := ListAll(context.Background(), catalog, client, ""); err == nil {
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
