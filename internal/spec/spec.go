// Package spec parses the `+` install grammar, e.g. "gemma4+cuda+webui" or
// "+nvidia-drivers". Tokens are classified by dimension (engine, accelerator,
// quant, addon, component) regardless of order; the first non-typed token is
// the base (model).
package spec

import "strings"

// Spec is a parsed install request.
type Spec struct {
	Raw        string
	Base       string   // model id, e.g. "gemma4" ("" for component-only specs)
	Engine     string   // llama.cpp | vllm | openvino | ollama
	Accel      string   // cuda | rocm | vulkan | npu | intel-gpu | cpu
	Quant      string   // q4 | q8 | int4 | int8 | fp16 ...
	Addons     []string // webui | api | openai-api | bench
	Components []string // nvidia-drivers | rocm | cuda-toolkit | intel-npu ...
}

var (
	engines      = set("llama.cpp", "vllm", "openvino", "ollama")
	accelerators = set("cuda", "rocm", "vulkan", "npu", "intel-gpu", "metal", "cpu")
	quants       = set("q4", "q8", "q4_k_m", "q5", "q6", "int4", "int8", "fp16", "bf16")
	addons       = set("webui", "api", "openai-api", "bench")
	components   = set("nvidia-drivers", "cuda-toolkit", "intel-npu", "intel-gpu-driver", "rocm-runtime")
)

// Parse splits s on '+' and classifies each token.
func Parse(s string) Spec {
	sp := Spec{Raw: s}
	tokens := strings.Split(s, "+")
	for idx, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		switch {
		case engines[t]:
			sp.Engine = t
		case accelerators[t]:
			sp.Accel = t
		case quants[t]:
			sp.Quant = t
		case addons[t]:
			sp.Addons = append(sp.Addons, t)
		case components[t]:
			sp.Components = append(sp.Components, t)
		case idx == 0 && sp.Base == "":
			// leading token that isn't a known dimension → it's the base model
			sp.Base = t
		default:
			// Unknown token following a base: treat ambiguous as an accelerator
			// hint if none set, else an addon, so users aren't blocked.
			if sp.Base == "" {
				sp.Base = t
			} else if sp.Accel == "" {
				sp.Accel = t
			} else {
				sp.Addons = append(sp.Addons, t)
			}
		}
	}
	return sp
}

// ComponentOnly reports whether the spec installs only components/drivers (no model).
func (s Spec) ComponentOnly() bool {
	return s.Base == "" && len(s.Components) > 0
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
