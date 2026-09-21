package snapcatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func WriteFakeCatalog(t testing.TB, entries string) *Reader {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, []byte(entries), 0o644); err != nil {
		t.Fatalf("writing catalog: %v", err)
	}
	return &Reader{Path: path}
}
