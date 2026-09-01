package catalog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// ParseRemoteCatalog decodes the publicly published catalog JSON. It rejects
// empty or malformed catalog snapshots.
func ParseRemoteCatalog(data []byte) (RemoteCatalog, error) {
	var cat RemoteCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return RemoteCatalog{}, fmt.Errorf("parsing catalog JSON: %w", err)
	}
	if len(cat.Repositories) == 0 {
		return RemoteCatalog{}, fmt.Errorf("catalog JSON contains no repositories")
	}
	return cat, nil
}

// PublicRepositories returns the repositories whose visibility is exactly
// "public", sorted by full name so processing order (and therefore output)
// does not depend on Go's randomized map iteration order.
func PublicRepositories(cat RemoteCatalog) []RemoteRepository {
	repos := make([]RemoteRepository, 0, len(cat.Repositories))
	for _, repo := range cat.Repositories {
		if repo.Visibility == "public" {
			repos = append(repos, repo)
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].FullName < repos[j].FullName })
	return repos
}

// snapNamePattern matches snapd's snap name rules: lowercase letters,
// digits, and single dashes, with no leading/trailing/doubled dash.
var snapNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateSnapName reports whether name is a syntactically valid snap name.
func ValidateSnapName(name string) error {
	if name == "" {
		return fmt.Errorf("snap name is empty")
	}
	if len(name) > 40 {
		return fmt.Errorf("snap name %q exceeds 40 characters", name)
	}
	if !snapNamePattern.MatchString(name) {
		return fmt.Errorf("snap name %q is not a valid snap name", name)
	}
	return nil
}

// snapcraftRoot models only the field this parser needs from
// snap/snapcraft.yaml.
type snapcraftRoot struct {
	Name string `yaml:"name"`
}

// ParseSnapcraftName extracts and validates the authoritative root-level
// `name` field from a snap/snapcraft.yaml file's contents. The name must be
// a non-empty literal scalar.
func ParseSnapcraftName(data []byte) (string, error) {
	var root snapcraftRoot
	if err := yaml.Unmarshal(data, &root); err != nil {
		return "", fmt.Errorf("parsing snapcraft.yaml: %w", err)
	}
	if root.Name == "" {
		return "", fmt.Errorf("snapcraft.yaml has no root-level name")
	}
	if err := ValidateSnapName(root.Name); err != nil {
		return "", fmt.Errorf("snapcraft.yaml name: %w", err)
	}
	return root.Name, nil
}
