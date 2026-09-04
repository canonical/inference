package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
)

func TestValidateSnapName(t *testing.T) {
	path := filepath.Join(t.TempDir(), snapcatalog.Filename)
	if err := os.WriteFile(path, []byte(`[{"snap":"smollm2"}]`), 0o600); err != nil {
		t.Fatalf("writing catalog: %v", err)
	}
	catalog := &snapcatalog.Reader{Path: path}

	if err := ValidateSnapName(catalog, "smollm2"); err != nil {
		t.Fatalf("validating known provider: %v", err)
	}
	err := ValidateSnapName(catalog, "unrelated-snap")
	if err == nil || !strings.Contains(err.Error(), `unknown inference provider "unrelated-snap"`) {
		t.Fatalf("got %v, want unknown provider error", err)
	}
}
