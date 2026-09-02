package snapcatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const Filename = "onboarded-snaps.json"

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
	CommonPath string
	SnapPath   string
}

func NewReader() Reader {
	return Reader{
		CommonPath: catalogPath(os.Getenv("SNAP_COMMON")),
		SnapPath:   catalogPath(os.Getenv("SNAP")),
	}
}

func (r Reader) Read() ([]Entry, error) {
	path := r.CommonPath
	if path == "" || !fileExists(path) {
		path = r.SnapPath
	}
	if path == "" {
		return nil, fmt.Errorf("snap catalog not found: neither SNAP_COMMON nor SNAP is set")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading snap catalog from %s: %w", path, err)
	}
	return ParseEntries(data)
}

func catalogPath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, Filename)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
