package tray

import (
	"testing"

	"inference/internal/backend"
)

func bk(name string, online bool, kind string, models ...string) backend.Backend {
	return backend.Backend{Name: name, Online: online, Kind: kind, Models: models}
}

func TestDeriveState(t *testing.T) {
	cases := []struct {
		name string
		up   bool
		bs   []backend.Backend
		want State
	}{
		{"proxy down", false, []backend.Backend{bk("g", true, "local-snap")}, StateIdle},
		{"up, none online", true, []backend.Backend{bk("g", false, "local-snap")}, StateAttention},
		{"up, no backends", true, nil, StateAttention},
		{"up, one online", true, []backend.Backend{bk("g", true, "local-snap")}, StateActive},
	}
	for _, c := range cases {
		if got := DeriveState(c.up, c.bs); got != c.want {
			t.Errorf("%s: DeriveState = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestModelIDsDeduped(t *testing.T) {
	bs := []backend.Backend{
		bk("g", true, "local-snap", "gemma4", "gemma4-vision"),
		bk("a", true, "remote", "gemma4", "claude"),
	}
	got := ModelIDs(bs)
	want := []string{"gemma4", "gemma4-vision", "claude"}
	if len(got) != len(want) {
		t.Fatalf("ModelIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ModelIDs = %v, want %v", got, want)
		}
	}
}

func TestInstalledNamesLocalOnly(t *testing.T) {
	bs := []backend.Backend{
		bk("gemma4", true, "local-snap", "gemma4"),
		bk("anthropic", true, "remote", "claude"),
	}
	got := InstalledNames(bs)
	if len(got) != 1 || got[0] != "gemma4" {
		t.Errorf("InstalledNames = %v, want [gemma4]", got)
	}
}

func TestHeaderLine(t *testing.T) {
	if got := HeaderLine(false, nil); got != "Inference — proxy stopped" {
		t.Errorf("down header = %q", got)
	}
	bs := []backend.Backend{bk("g", true, "local-snap"), bk("q", false, "local-snap")}
	if got := HeaderLine(true, bs); got != "Inference — proxy running  (1/2 online)" {
		t.Errorf("up header = %q", got)
	}
}
