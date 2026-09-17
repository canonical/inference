package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
)

func TestIsSnap(t *testing.T) {
	for _, test := range []struct {
		name     string
		snap     string
		snapName string
		want     bool
	}{
		{name: "inference snap", snap: "/snap/inference/current", snapName: InferenceSnapName, want: true},
		{name: "outside snap", snapName: InferenceSnapName},
		{name: "different snap", snap: "/snap/other/current", snapName: "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SNAP", test.snap)
			t.Setenv("SNAP_NAME", test.snapName)

			if got := IsSnap(); got != test.want {
				t.Fatalf("got %t, want %t", got, test.want)
			}
		})
	}
}

func TestValidateSnapName(t *testing.T) {
	path := filepath.Join(t.TempDir(), snapcatalog.Filename)
	if err := os.WriteFile(path, []byte(`[{"snap":"smollm2"}]`), 0o600); err != nil {
		t.Fatalf("writing catalog: %v", err)
	}
	catalog := &snapcatalog.Reader{Path: path}

	if err := ValidateSnapName(catalog, "smollm2"); err != nil {
		t.Fatalf("validating known snap: %v", err)
	}
	err := ValidateSnapName(catalog, "unrelated-snap")
	if err == nil || !strings.Contains(err.Error(), `unknown inference snap "unrelated-snap"`) {
		t.Fatalf("got %v, want unknown snap error", err)
	}
}
