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
	LocalPath  string
}

func NewReader() *Reader {
	return &Reader{
		CommonPath: catalogPath(os.Getenv("SNAP_COMMON")),
		SnapPath:   catalogPath(os.Getenv("SNAP")),
		LocalPath:  Filename,
	}
}

func (r Reader) Read() ([]Entry, error) {
	path := r.CommonPath
	if path == "" || !fileExists(path) {
		path = r.SnapPath
	}
	if path == "" || !fileExists(path) {
		path = r.LocalPath
	}
	if path == "" {
		return nil, fmt.Errorf("snap catalog not found: no catalog path is configured")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading snap catalog from %s: %w", path, err)
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
