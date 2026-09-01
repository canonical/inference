// Package providers defines the provider domain model and the logic that
// joins the provider catalog with installed-snap state reported by snapd.
package providers

// Type identifies the kind of provider. It is a named string so additional
// provider types (for example, configured OpenAI-compatible endpoints) can be
// introduced later without changing the public shape of existing values.
type Type string

// InferenceSnap is the only provider type supported by this slice: a
// provider backed by an installable inference snap.
const InferenceSnap Type = "inference-snap"

// StatusNotInstalled is the only status synthesized by the provider service.
// Every other status string is preserved verbatim from snapd.
const StatusNotInstalled = "not installed"

// Provider is a single row in `inference providers` output. Status is kept as
// a plain string because installed statuses are owned by snapd; this package
// must not translate or reinterpret them.
type Provider struct {
	Name   string `json:"provider"`
	Type   Type   `json:"type"`
	Status string `json:"status"`
}

// Installed reports whether the provider is anything other than not
// installed.
func (p Provider) Installed() bool {
	return p.Status != StatusNotInstalled
}
