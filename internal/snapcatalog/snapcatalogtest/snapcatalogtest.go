package snapcatalogtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
)

func WriteCatalog(t testing.TB, entries string) *snapcatalog.Reader {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, snapcatalog.Filename)
	if err := os.WriteFile(path, []byte(entries), 0o644); err != nil {
		t.Fatalf("writing catalog: %v", err)
	}
	return &snapcatalog.Reader{Path: path}
}
