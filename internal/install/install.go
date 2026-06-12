// Package install resolves `+` specs into an explicit, confirm-once plan
// (snaps, drivers, addons) and can execute the snap-install steps. Driver steps
// are advisory in this first pass (printed, never auto-run).
package install

import (
	"fmt"
	"strings"

	"inference/internal/catalogue"
	"inference/internal/hardware"
	"inference/internal/spec"
	"inference/internal/ui"
)

// driverAdvice maps an accelerator to a component name + install hint.
var driverAdvice = map[string]struct{ comp, hint string }{
	"cuda":      {"nvidia-drivers", "sudo ubuntu-drivers install"},
	"rocm":      {"rocm-runtime", "sudo apt install rocm"},
	"intel-gpu": {"intel-gpu-driver", "sudo apt install intel-opencl-icd"},
	"npu":       {"intel-npu", "sudo apt install intel-npu-driver"},
}

// Step is one unit of the plan.
type Step struct {
	Kind   string // snap-install | driver | addon | engine | register
	Detail string
	Cmd    string // command (snap-install) or advice (driver)
	Manual bool   // advisory only — not auto-executed
}

// Plan is the resolved set of steps plus warnings.
type Plan struct {
	Steps    []Step
	Warnings []string
}

// Resolve turns specs into a plan for this host.
func Resolve(specs []spec.Spec, hw *hardware.Info) *Plan {
	p := &Plan{}
	hwAccels := hw.Accelerators()
	for _, sp := range specs {
		if sp.ComponentOnly() {
			for _, c := range sp.Components {
				p.Steps = append(p.Steps, Step{
					Kind: "driver", Detail: "install component " + c,
					Cmd: hintForComponent(c), Manual: true,
				})
			}
			continue
		}
		if sp.Base == "" {
			p.Warnings = append(p.Warnings, "empty spec ignored")
			continue
		}
		supported, known := catalogue.Accels(sp.Base)
		accel, ok := chooseAccel(sp.Accel, supported, hwAccels)

		if known {
			if sp.Accel != "" && !ok {
				p.Warnings = append(p.Warnings, fmt.Sprintf(
					"%s has no %s build; supported: %s — falling back to %s",
					sp.Base, sp.Accel, strings.Join(supported, ", "), accel))
				accel, _ = chooseAccel("", supported, hwAccels)
			}
			// Driver advice if the accelerator needs a runtime we likely lack.
			if needsDriver(accel, hw) {
				if adv, has := driverAdvice[accel]; has {
					p.Warnings = append(p.Warnings, fmt.Sprintf(
						"%s acceleration needs the %s runtime", accel, adv.comp))
					p.Steps = append(p.Steps, Step{
						Kind: "driver", Detail: adv.comp + " (" + accel + ")",
						Cmd: adv.hint, Manual: true,
					})
				}
			}
			p.Steps = append(p.Steps, Step{
				Kind:   "snap-install",
				Detail: fmt.Sprintf("%s  (%s build)", sp.Base, accel),
				Cmd:    "snap install " + sp.Base,
			})
			p.Steps = append(p.Steps, Step{
				Kind: "register", Detail: "register " + sp.Base + " with the proxy", Manual: true,
			})
		} else {
			// Hybrid path: no model snap — would install an engine + weights.
			engine := sp.Engine
			if engine == "" {
				engine = "llama.cpp"
			}
			p.Warnings = append(p.Warnings, fmt.Sprintf(
				"%s is not a packaged inference snap", sp.Base))
			p.Steps = append(p.Steps, Step{
				Kind: "engine", Manual: true,
				Detail: fmt.Sprintf("hybrid path: install %s (%s) + pull %s weights  [Milestone 2]",
					engine, accel, sp.Base),
			})
		}

		for _, a := range sp.Addons {
			p.Steps = append(p.Steps, Step{
				Kind: "addon", Manual: true,
				Detail: "addon " + a + "  [Milestone 2]",
			})
		}
	}
	return p
}

// Print renders the plan.
func (p *Plan) Print() {
	for _, w := range p.Warnings {
		ui.Printf("  %s %s\n", ui.Yellow(ui.SymWarn), w)
	}
	if len(p.Steps) == 0 {
		ui.Println(ui.Dim("  (nothing to do)"))
		return
	}
	ui.Printf("\n  %s\n", ui.Bold("Plan"))
	t := ui.NewTable().Indent("    ")
	for _, s := range p.Steps {
		mark := ui.Green("+")
		note := ""
		switch {
		case s.Manual && s.Kind == "driver":
			mark = ui.Yellow("!")
			note = ui.Dim(ui.SymArrow + " " + s.Cmd)
		case s.Manual:
			mark = ui.Dim(ui.SymArrow)
		}
		t.Row(mark, s.Detail, note)
	}
	t.Render()
}

// SnapNames returns the snaps this plan would install (non-manual steps).
func (p *Plan) SnapNames() []string {
	var names []string
	for _, s := range p.Steps {
		if s.Kind == "snap-install" && !s.Manual {
			fields := strings.Fields(s.Cmd) // "snap install <name>"
			names = append(names, fields[len(fields)-1])
		}
	}
	return names
}

// HasInstallSteps reports whether the plan has anything to auto-execute.
func (p *Plan) HasInstallSteps() bool {
	for _, s := range p.Steps {
		if s.Kind == "snap-install" && !s.Manual {
			return true
		}
	}
	return false
}

func chooseAccel(requested string, supported, hwAccels []string) (string, bool) {
	if requested != "" {
		return requested, contains(supported, requested)
	}
	for _, a := range hwAccels {
		if contains(supported, a) {
			return a, true
		}
	}
	return "cpu", contains(supported, "cpu")
}

// needsDriver is a heuristic: does this accelerator likely require a runtime the
// host doesn't already have?
func needsDriver(accel string, hw *hardware.Info) bool {
	switch accel {
	case "cpu":
		return false
	case "cuda":
		for _, g := range hw.GPUs {
			if g.Vendor == "nvidia" && g.Driver != "" {
				return false // driver already present
			}
		}
		return true
	case "npu":
		return hw.NPU == ""
	default:
		return false // intel-gpu/rocm: assume mesa/runtime present; refined later
	}
}

func hintForComponent(c string) string {
	for _, adv := range driverAdvice {
		if adv.comp == c {
			return adv.hint
		}
	}
	return "snap install " + c
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
