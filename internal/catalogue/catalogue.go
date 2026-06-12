// Package catalogue is the curated list of official inference model snaps and
// the accelerators each ships a build for. It is the single source of truth used
// by discovery, the resolver, and the `catalogue` command.
package catalogue

import "strings"

// Model is a catalogue entry.
type Model struct {
	Name       string   // snap name, e.g. "gemma3"
	Summary    string   // short description
	Modalities string   // e.g. "text + vision"
	Accels     []string // accelerators with a shipped build, best-first
}

// Models is the curated catalogue. Keep ordered roughly by recommendation.
var Models = []Model{
	{"gemma4", "Google Gemma 4", "text + vision", []string{"cuda", "cpu"}},
	{"gemma3", "Google Gemma 3", "text + vision", []string{"cuda", "rocm", "intel-gpu", "cpu"}},
	{"deepseek-r1", "DeepSeek R1, reasoning", "text", []string{"cuda", "intel-gpu", "npu", "cpu"}},
	{"qwen-vl", "Qwen VL, vision-language", "text + vision", []string{"cuda", "intel-gpu", "npu", "cpu"}},
	{"nemotron-3-nano", "NVIDIA Nemotron 3 Nano", "text, reasoning", []string{"cuda", "cpu"}},
	{"nemotron-3-nano-omni", "Nemotron 3 Nano Omni", "text + image + audio + video", []string{"cuda", "cpu"}},
}

// Get returns the catalogue entry for a snap name.
func Get(name string) (Model, bool) {
	for _, m := range Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// Accels returns the accelerators a model ships, and whether it is in the catalogue.
func Accels(name string) ([]string, bool) {
	m, ok := Get(name)
	return m.Accels, ok
}

// Names returns all catalogue snap names.
func Names() []string {
	out := make([]string, len(Models))
	for i, m := range Models {
		out[i] = m.Name
	}
	return out
}

// Search returns catalogue entries matching query by substring (name/summary)
// or close spelling — so "gema" and "qwen vl" both find their models.
func Search(query string) []Model {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return Models
	}
	var out []Model
	for _, m := range Models {
		n := strings.ToLower(m.Name)
		if strings.Contains(n, q) || strings.Contains(strings.ToLower(m.Summary), q) || distance(q, n) <= 3 {
			out = append(out, m)
		}
	}
	return out
}

// Closest returns the catalogue model whose name is nearest to name, and the
// edit distance (0 = exact match).
func Closest(name string) (Model, int) {
	best, bestD := Model{}, 1<<30
	ln := strings.ToLower(name)
	for _, m := range Models {
		if d := distance(ln, strings.ToLower(m.Name)); d < bestD {
			best, bestD = m, d
		}
	}
	return best, bestD
}

// Nearest returns the option nearest to name by edit distance.
func Nearest(name string, options []string) (string, int) {
	best, bestD := "", 1<<30
	ln := strings.ToLower(name)
	for _, o := range options {
		if d := distance(ln, strings.ToLower(o)); d < bestD {
			best, bestD = o, d
		}
	}
	return best, bestD
}

// distance is the Levenshtein edit distance between a and b.
func distance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
