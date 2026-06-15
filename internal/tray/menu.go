// Package tray renders an Ubuntu top-bar (StatusNotifierItem) indicator for the
// inference daemon. It is a thin, unprivileged client: it only reads and drives
// the daemon over its localhost HTTP API, exactly like the CLI.
package tray

import (
	"fmt"

	"inference/internal/backend"
)

// State is the indicator's visual status, derived from the daemon snapshot.
type State int

const (
	// StateIdle: the proxy is not reachable.
	StateIdle State = iota
	// StateAttention: the proxy is up but nothing is serving.
	StateAttention
	// StateActive: the proxy is up and at least one backend is online.
	StateActive
)

// DeriveState maps a daemon snapshot to an indicator state. Pure; unit-tested.
func DeriveState(up bool, bs []backend.Backend) State {
	if !up {
		return StateIdle
	}
	if OnlineCount(bs) == 0 {
		return StateAttention
	}
	return StateActive
}

// OnlineCount returns how many backends are currently online.
func OnlineCount(bs []backend.Backend) int {
	n := 0
	for _, b := range bs {
		if b.Online {
			n++
		}
	}
	return n
}

// HeaderLine is the disabled menu title summarising current status. Pure.
func HeaderLine(up bool, bs []backend.Backend) string {
	if !up {
		return "Inference — proxy stopped"
	}
	return fmt.Sprintf("Inference — proxy running  (%d/%d online)", OnlineCount(bs), len(bs))
}

// BackendLine formats one backend row for the menu. Pure.
func BackendLine(b backend.Backend) string {
	dot := "o"
	state := "offline"
	if b.Online {
		dot = "*"
		state = "online"
	}
	eng := b.Engine
	if eng == "" {
		eng = b.Kind
	}
	return fmt.Sprintf("%s %-22s %-8s %s", dot, b.Name, state, eng)
}

// ModelIDs returns the de-duplicated, ordered list of model ids across backends.
// Used to populate the "Default model" radio submenu. Pure.
func ModelIDs(bs []backend.Backend) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range bs {
		for _, m := range b.Models {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// InstalledNames returns the names of locally installed (local-snap) backends,
// for the "Remove model" submenu. Pure.
func InstalledNames(bs []backend.Backend) []string {
	var out []string
	for _, b := range bs {
		if b.Kind == "local-snap" {
			out = append(out, b.Name)
		}
	}
	return out
}
