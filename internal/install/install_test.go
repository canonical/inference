package install

import (
	"strings"
	"testing"

	"inference/internal/hardware"
	"inference/internal/spec"
)

// hwIntel mimics the test author's Lunar Lake box: intel-gpu + npu + cpu, no NVIDIA.
func hwIntel(profile string) *hardware.Info {
	return &hardware.Info{
		Profile: profile,
		NPU:     "intel_vpu",
		GPUs:    []hardware.GPU{{Name: "Intel Arc", Vendor: "intel", Accel: "intel-gpu"}},
	}
}

func resolve(t *testing.T, hw *hardware.Info, specStr string) *Plan {
	t.Helper()
	return Resolve([]spec.Spec{spec.Parse(specStr)}, hw)
}

func TestResolve_UnsupportedAccelIsError(t *testing.T) {
	// gemma4 has no rocm build -> hard error, no steps.
	p := resolve(t, hwIntel("laptop"), "gemma4+rocm")
	if !p.HasErrors() {
		t.Fatalf("expected an error for gemma4+rocm, got steps=%v", p.Steps)
	}
	if len(p.Steps) != 0 {
		t.Fatalf("expected no steps on error, got %v", p.Steps)
	}
	if !strings.Contains(p.Errors[0], "no rocm build") {
		t.Errorf("error should explain the missing build: %q", p.Errors[0])
	}
}

func TestResolve_AccelMissingOnHostIsError(t *testing.T) {
	// gemma4 supports cuda, but this box has no NVIDIA GPU.
	p := resolve(t, hwIntel("laptop"), "gemma4+cuda")
	if !p.HasErrors() {
		t.Fatalf("expected an error for cuda on a non-NVIDIA box")
	}
	if !strings.Contains(p.Errors[0], "try gemma4+") {
		t.Errorf("error should suggest an alternative: %q", p.Errors[0])
	}
}

func TestResolve_WebuiAddonInstallsSnap(t *testing.T) {
	p := resolve(t, hwIntel("laptop"), "gemma4+webui")
	if p.HasErrors() {
		t.Fatalf("unexpected errors: %v", p.Errors)
	}
	names := p.SnapNames()
	if !contains(names, "gemma4") || !contains(names, "open-webui") {
		t.Errorf("expected gemma4 + open-webui in install set, got %v", names)
	}
}

func TestResolve_EdgeProfilePicksTightQuant(t *testing.T) {
	p := resolve(t, hwIntel("edge"), "gemma4")
	if p.HasErrors() {
		t.Fatalf("unexpected errors: %v", p.Errors)
	}
	var detail string
	for _, s := range p.Steps {
		if s.Kind == "snap-install" {
			detail = s.Detail
		}
	}
	if !strings.Contains(detail, "q4") {
		t.Errorf("edge profile should default to q4 quant, got %q", detail)
	}
}
