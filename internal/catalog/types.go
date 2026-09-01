// Package catalog resolves the public inference-snap provider catalog:
// parsing the published repository list, resolving each repository's
// authoritative snap name from its snap/snapcraft.yaml, and caching the
// validated result with a TTL and offline fallbacks.
package catalog

import "time"

// SchemaVersion is the current resolved-snapshot schema version, used by
// both the on-disk cache envelope and the embedded seed.
const SchemaVersion = 1

// RemoteCatalog is the top-level shape of the publicly published catalog
// JSON at onboarded-snaps.generated.json.
type RemoteCatalog struct {
	Repositories map[string]RemoteRepository `json:"repositories"`
}

// RemoteRepository is one entry in the published catalog. The map key,
// ModelName, and repository basename are not authoritative provider
// identifiers; only the snap/snapcraft.yaml root name is.
type RemoteRepository struct {
	FullName   string `json:"full_name"`
	HTMLURL    string `json:"html_url"`
	ModelName  string `json:"model_name"`
	Visibility string `json:"visibility"`
}

// Provider is a resolved, authoritative catalog entry: a public repository
// together with the snap name declared in its snap/snapcraft.yaml.
type Provider struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
}

// Snapshot is a fully resolved and validated catalog snapshot, ready to
// cache or serve. Providers are sorted by Name.
type Snapshot struct {
	SchemaVersion int        `json:"schema_version"`
	FetchedAt     time.Time  `json:"fetched_at"`
	Providers     []Provider `json:"providers"`
}

// Names returns the provider names in the snapshot, in order.
func (s Snapshot) Names() []string {
	names := make([]string, len(s.Providers))
	for i, p := range s.Providers {
		names[i] = p.Name
	}
	return names
}
