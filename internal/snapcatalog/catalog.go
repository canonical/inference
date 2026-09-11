package snapcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	Filename = "onboarded-snaps.json"
	URL      = "https://canonical.github.io/inference-snaps-admin/onboarded-snaps.json"

	// EnvVar overrides the default catalog location under SNAP_COMMON.
	EnvVar = "INFERENCE_SNAPS_CATALOG"

	maxCatalogSizeBytes = 4 * 1024 * 1024
)

var ErrNotConfigured = errors.New("snap catalog path is not configured")

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
		return nil, fmt.Errorf("%w: set %s to the catalog file", ErrNotConfigured, EnvVar)
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

type Refresher struct {
	Client *http.Client
	Path   string
	URL    string
}

func NewRefresher(client *http.Client) *Refresher {
	return &Refresher{
		Client: client,
		Path:   DefaultPath(),
		URL:    URL,
	}
}

func (r Refresher) Refresh(ctx context.Context) error {
	if r.Path == "" {
		return fmt.Errorf("%w: set %s to the catalog file", ErrNotConfigured, EnvVar)
	}
	if r.URL == "" {
		return errors.New("snap catalog URL is not configured")
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return fmt.Errorf("creating snap catalog request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("fetching snap catalog: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("fetching snap catalog: HTTP %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogSizeBytes+1))
	if err != nil {
		return fmt.Errorf("reading snap catalog response: %w", err)
	}
	if len(data) > maxCatalogSizeBytes {
		return fmt.Errorf("snap catalog response exceeded %d bytes", maxCatalogSizeBytes)
	}
	if _, err := ParseEntries(data); err != nil {
		return err
	}
	if err := replaceFile(r.Path, data); err != nil {
		return fmt.Errorf("writing snap catalog to %s: %w", r.Path, err)
	}
	return nil
}

func replaceFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
