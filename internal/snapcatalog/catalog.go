package snapcatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	Filename = "onboarded-snaps.json"

	// EnvVar overrides the default catalog location under SNAP_COMMON.
	EnvVar = "INFERENCE_SNAPS_CATALOG"
)

type Entry struct {
	SnapName      string `json:"snap"`
	ModelName     string `json:"model_name"`
	Repository    string `json:"full_name"`
	RepositoryURL string `json:"html_url"`
}

func ParseEntries(data []byte) ([]Entry, error) {
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decoding published snap catalog: %w", err)
	}

	slices.SortFunc(entries, func(a, b Entry) int {
		return strings.Compare(a.SnapName, b.SnapName)
	})
	return entries, nil
}

type Reader struct {
	// An empty Path means no catalog is configured and reads fail.
	Path string
}

func NewReader() *Reader {
	return &Reader{Path: DefaultPath()}
}

func DefaultPath() string {
	if path := os.Getenv(EnvVar); path != "" {
		return path
	}
	if common := os.Getenv("SNAP_COMMON"); common != "" {
		return filepath.Join(common, Filename)
	}
	return ""
}

func (r Reader) Read() ([]Entry, error) {
	if r.Path == "" {
		return nil, fmt.Errorf("snap catalog path is not configured: set %s to the catalog file", EnvVar)
	}

	data, err := os.ReadFile(r.Path)
	if err != nil {
		return nil, fmt.Errorf("reading snap catalog from %s: %w", r.Path, err)
	}
	return ParseEntries(data)
}

func (r Reader) Contains(name string) (bool, error) {
	entries, err := r.Read()
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(entries, func(entry Entry) bool {
		return entry.SnapName == name
	}), nil
}
