// Package install resolves `+` specs into an explicit, confirm-once plan
// (snaps, drivers, addons) and can execute the snap-install steps. Driver steps
// are advisory in this first pass (printed, never auto-run).
package install

import (
	"fmt"
	"sort"
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

// accelHardware names the silicon each accelerator needs, for clear errors.
var accelHardware = map[string]string{
	"cuda":      "NVIDIA GPU",
	"rocm":      "AMD GPU",
	"intel-gpu": "Intel GPU",
	"npu":       "NPU",
	"vulkan":    "Vulkan-capable GPU",
	"metal":     "Apple GPU",
}

// addonSnaps maps a `+addon` token to the snap that provides it and a one-line
// note on how it's wired to the proxy.
var addonSnaps = map[string]struct{ snap, note string }{
	"webui": {"open-webui", "point Open WebUI at the proxy (OPENAI_API_BASE_URL=http://localhost:8080/v1)"},
}

// AddonSnap returns the snap that provides an addon (e.g. "webui" -> "open-webui").
func AddonSnap(addon string) (string, bool) {
	reg, ok := addonSnaps[addon]
	return reg.snap, ok
}

// defaultQuant tunes weight precision by machine profile (tighter on small boxes).
func defaultQuant(profile string) string {
	switch profile {
	case "edge":
		return "q4"
	case "laptop":
		return "q4"
	case "server":
		return "fp16"
	default: // workstation
		return "q8"
	}
}

// Step is one unit of the plan.
type Step struct {
	Kind   string // snap-install | driver | addon | engine | register
	Detail string
	Cmd    string // command (snap-install) or advice (driver)
	Manual bool   // advisory only — not auto-executed
}

// Plan is the resolved set of steps plus warnings and hard errors. A plan with
// errors is not auto-executed — the caller surfaces them and stops.
type Plan struct {
	Steps    []Step
	Warnings []string
	Errors   []string
}

// Resolve turns specs into a plan for this host. The host profile
// (hw.Profile, possibly overridden by --profile) tunes accelerator preference
// and default quant.
func Resolve(specs []spec.Spec, hw *hardware.Info) *Plan {
	p := &Plan{}
	hwAccels := preferredAccels(hw)
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

		// A requested accelerator that the base doesn't build for is a hard error
		// (naming the alternatives), not a silent fallback.
		if known && sp.Accel != "" && !contains(supported, sp.Accel) {
			p.Errors = append(p.Errors, fmt.Sprintf(
				"%s has no %s build — available: %s; try %s",
				sp.Base, sp.Accel, strings.Join(supported, ", "),
				suggest(sp.Base, supported, hwAccels)))
			continue
		}
		// A requested accelerator the base supports but this machine lacks is also
		// a hard error (e.g. +cuda on a box with no NVIDIA GPU): the build exists,
		// the silicon to run it doesn't, and we won't silently fall back to CPU.
		if sp.Accel != "" && sp.Accel != "cpu" && !contains(hwAccels, sp.Accel) {
			need := accelHardware[sp.Accel]
			if need == "" {
				need = sp.Accel + " accelerator"
			}
			lead := fmt.Sprintf("%s requested", sp.Accel)
			if known && contains(supported, sp.Accel) {
				lead = fmt.Sprintf("%s has a %s build", sp.Base, sp.Accel)
			}
			p.Errors = append(p.Errors, fmt.Sprintf(
				"%s, but this machine has no %s — detected: %s; try %s",
				lead, need, strings.Join(hw.Accelerators(), ", "),
				suggest(sp.Base, supported, hwAccels)))
			continue
		}

		accel, _ := chooseAccel(sp.Accel, supported, hwAccels)
		quant := sp.Quant
		if quant == "" {
			quant = defaultQuant(hw.Profile)
		}

		if known {
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
				Detail: fmt.Sprintf("%s  (%s, %s)", sp.Base, accel, quant),
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
				Detail: fmt.Sprintf("hybrid path: install %s (%s, %s) + pull %s weights  [Milestone 2]",
					engine, accel, quant, sp.Base),
			})
		}

		// Addons: resolve known ones to a real snap-install step + a wiring note.
		for _, a := range sp.Addons {
			if reg, ok := addonSnaps[a]; ok {
				p.Steps = append(p.Steps, Step{
					Kind: "snap-install", Detail: a + " addon: " + reg.snap,
					Cmd: "snap install " + reg.snap,
				})
				p.Steps = append(p.Steps, Step{
					Kind: "addon", Manual: true, Detail: reg.note,
				})
			} else {
				p.Steps = append(p.Steps, Step{
					Kind: "addon", Manual: true, Detail: "addon " + a + "  [not yet available]",
				})
			}
		}
	}
	return p
}

// suggest returns a friendly "base+accel" alternative: the first accelerator the
// base supports that this machine actually has, else base+cpu.
func suggest(base string, supported, hwAccels []string) string {
	for _, a := range hwAccels {
		if contains(supported, a) {
			return base + "+" + a
		}
	}
	return base + "+cpu"
}

// preferredAccels orders the machine's accelerators by profile: edge/laptop
// prefer the low-power path (npu/cpu) before a discrete/integrated GPU.
func preferredAccels(hw *hardware.Info) []string {
	accels := hw.Accelerators()
	if hw.Profile != "edge" {
		return accels
	}
	rank := map[string]int{"npu": 0, "cpu": 1}
	out := append([]string(nil), accels...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, oki := rank[out[i]]
		rj, okj := rank[out[j]]
		if !oki {
			ri = 9
		}
		if !okj {
			rj = 9
		}
		return ri < rj
	})
	return out
}

// HasErrors reports whether the plan has hard errors and must not be executed.
func (p *Plan) HasErrors() bool { return len(p.Errors) > 0 }

// Print renders the plan.
func (p *Plan) Print() {
	for _, e := range p.Errors {
		ui.Printf("  %s %s\n", ui.Red(ui.SymErr), e)
	}
	for _, w := range p.Warnings {
		ui.Printf("  %s %s\n", ui.Yellow(ui.SymWarn), w)
	}
	if len(p.Steps) == 0 {
		if len(p.Errors) == 0 {
			ui.Println(ui.Dim("  (nothing to do)"))
		}
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
