// Package backend discovers routable inference backends: locally installed
// inference model snaps (via `snap get -d`) and external providers from config.
// Each backend exposes an OpenAI-compatible base URL; the proxy and chat client
// route to them by model id.
package backend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"inference/internal/config"
	"inference/internal/snapd"
)

// Backend is a routable OpenAI-compatible target.
type Backend struct {
	Name    string   // logical name, e.g. "qwen-vl", "anthropic"
	Kind    string   // local-snap | remote
	BaseURL string   // includes version path, no trailing slash (e.g. http://127.0.0.1:8326/v3)
	APIKey  string   // bearer token for remote backends
	Engine  string   // e.g. "openvino/intel-gpu"
	Source  string   // how it was discovered, e.g. "snap:qwen-vl"
	Models  []string // model ids this backend serves
	Online  bool
}

// KnownSnaps is the registry of official inference model snaps to probe.
var KnownSnaps = []string{
	"gemma4", "gemma3", "deepseek-r1",
	"nemotron-3-nano", "nemotron-3-nano-omni", "qwen-vl",
}

// snapCfg mirrors the relevant subset of an inference snap's `snap get -d` output.
type snapCfg struct {
	Cache struct {
		ActiveEngine string `json:"active-engine"`
	} `json:"cache"`
	Config struct {
		Engine struct {
			HTTP struct {
				BasePath string `json:"base-path"`
			} `json:"http"`
			ModelName string `json:"model-name"`
			Server    string `json:"server"`
		} `json:"engine"`
		Package struct {
			HTTP struct {
				Host string `json:"host"`
				Port int    `json:"port"`
			} `json:"http"`
		} `json:"package"`
		User struct {
			HTTP struct {
				Host string `json:"host"`
				Port int    `json:"port"`
			} `json:"http"`
		} `json:"user"`
	} `json:"config"`
}

// Discover returns all routable backends. It probes every installed snap (plus
// the known catalogue) and keeps those that serve an OpenAI endpoint; catalogue
// snaps are kept even when not yet responding so the user sees them as offline.
func Discover(cfg *config.Config) []Backend {
	var out []Backend

	known := map[string]bool{}
	for _, n := range KnownSnaps {
		known[n] = true
	}
	candidates := map[string]bool{} // dedup
	for _, n := range snapd.Installed() {
		candidates[n] = true
	}
	for _, n := range KnownSnaps {
		candidates[n] = true
	}
	var names []string
	for n := range candidates {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		b, ok := snapBackend(name)
		if !ok {
			continue
		}
		if known[name] || b.Online {
			out = append(out, b)
		}
	}

	for _, p := range cfg.Providers {
		b := Backend{
			Name:    p.Name,
			Kind:    "remote",
			BaseURL: strings.TrimRight(p.BaseURL, "/"),
			APIKey:  p.APIKey,
			Engine:  p.Type,
			Source:  "config",
			Models:  p.Models,
			Online:  p.APIKey != "" || p.Type == "openai-local",
		}
		out = append(out, b)
	}
	return out
}

// snapBackend reads an installed snap's config and builds a backend, probing it
// for live models. Returns ok=false if the snap is not installed/configured.
func snapBackend(name string) (Backend, bool) {
	raw, ok := snapd.Conf(name)
	if !ok {
		return Backend{}, false
	}
	var sc snapCfg
	if err := json.Unmarshal(raw, &sc); err != nil {
		return Backend{}, false
	}
	host := firstNonEmpty(sc.Config.User.HTTP.Host, sc.Config.Package.HTTP.Host, "127.0.0.1")
	port := sc.Config.Package.HTTP.Port
	if sc.Config.User.HTTP.Port != 0 {
		port = sc.Config.User.HTTP.Port
	}
	if port == 0 {
		return Backend{}, false
	}
	root := fmt.Sprintf("http://%s:%d", host, port)

	engine := sc.Config.Engine.Server
	if engine == "" {
		engine = "openvino" // these snaps front an OpenVINO Model Server
	}
	if sc.Cache.ActiveEngine != "" {
		engine = strings.TrimSuffix(engine, "-model-server") + "/" + sc.Cache.ActiveEngine
	}
	b := Backend{
		Name:   name,
		Kind:   "local-snap",
		Engine: engine,
		Source: "snap:" + name,
	}

	// The OpenAI base path isn't reliably in config, so probe the common ones
	// (OVMS serves /v3, llama.cpp/standard serve /v1) plus any config hint, and
	// take the first that returns a model list.
	var paths []string
	if bp := strings.Trim(sc.Config.Engine.HTTP.BasePath, "/"); bp != "" {
		paths = append(paths, "/"+bp)
	}
	paths = append(paths, "/v3", "/v1", "")
	for _, p := range paths {
		if models, ok := fetchModels(root+p, ""); ok {
			b.BaseURL = root + p
			b.Models = models
			b.Online = true
			break
		}
	}
	if b.BaseURL == "" {
		// Installed but not yet serving (e.g. still downloading weights): record a
		// best-guess URL and the configured model name if present.
		b.BaseURL = root + paths[0]
		if sc.Config.Engine.ModelName != "" {
			b.Models = []string{sc.Config.Engine.ModelName}
		}
	}
	return b, true
}

// fetchModels GETs {base}/models and returns the served model ids.
func fetchModels(base, apiKey string) ([]string, bool) {
	req, err := http.NewRequest(http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, false
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, false
	}
	var doc struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, false
	}
	var ids []string
	for _, m := range doc.Data {
		ids = append(ids, m.ID)
	}
	return ids, len(ids) > 0
}

// FindForModel resolves an alias and locates the backend serving model id.
// It returns the backend and the concrete (alias-resolved) model id.
func FindForModel(backends []Backend, aliases map[string]string, model string) (*Backend, string) {
	resolved := model
	if t, ok := aliases[model]; ok {
		resolved = t
	}
	for i := range backends {
		for _, m := range backends[i].Models {
			if m == resolved {
				return &backends[i], resolved
			}
		}
	}
	// If the model id wasn't found but matches a backend *name*, use its first model.
	for i := range backends {
		if backends[i].Name == resolved && len(backends[i].Models) > 0 {
			return &backends[i], backends[i].Models[0]
		}
	}
	return nil, resolved
}

// Default returns the first online backend's first model, or "".
func Default(backends []Backend) (*Backend, string) {
	for i := range backends {
		if backends[i].Online && len(backends[i].Models) > 0 {
			return &backends[i], backends[i].Models[0]
		}
	}
	return nil, ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
