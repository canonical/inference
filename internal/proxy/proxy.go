// Package proxy implements the federated OpenAI-compatible gateway: a single
// endpoint that routes /v1/* requests to the right backend by model id,
// translating each backend's base path and streaming responses through.
package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"inference/internal/backend"
	"inference/internal/config"
	"inference/internal/snapd"
	"inference/internal/ui"
)

// Server is the running proxy.
type Server struct {
	cfg      *config.Config
	mu       sync.RWMutex
	backends []backend.Backend
	client   *http.Client
}

// New builds a Server and performs an initial discovery.
func New(cfg *config.Config) *Server {
	s := &Server{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Minute}, // long: generation can be slow
	}
	s.refresh()
	return s
}

func (s *Server) refresh() {
	// Reload config from disk so aliases/providers added via the CLI take effect
	// without a restart; keep the old config if the reload fails.
	cfg := s.cfg
	if c, err := config.Load(); err == nil {
		cfg = c
	}
	bs := backend.Discover(cfg)
	s.mu.Lock()
	s.cfg = cfg
	s.backends = bs
	s.mu.Unlock()
}

func (s *Server) snapshot() []backend.Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backends
}

func (s *Server) conf() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// ListenAndServe starts the HTTP server (blocking) and a background refresher.
func (s *Server) ListenAndServe() error {
	go func() {
		t := time.NewTicker(20 * time.Second)
		for range t.C {
			s.refresh()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/chat/completions", s.handleProxy)
	mux.HandleFunc("/v1/completions", s.handleProxy)
	mux.HandleFunc("/v1/embeddings", s.handleProxy)
	// Management endpoints the (unprivileged) CLI delegates to, since only this
	// daemon runs as root and may read configs / install snaps via snapd.
	mux.HandleFunc("/v1/backends", s.handleBackends)
	mux.HandleFunc("/v1/install", func(w http.ResponseWriter, r *http.Request) {
		s.handleChange(w, r, snapd.Install)
	})
	mux.HandleFunc("/v1/remove", func(w http.ResponseWriter, r *http.Request) {
		s.handleChange(w, r, snapd.Remove)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`)
	})

	addr := fmt.Sprintf("%s:%d", s.cfg.Proxy.Bind, s.cfg.Proxy.Port)
	ui.Printf("%s proxy listening on http://%s/v1\n", ui.Green(ui.SymOK), addr)
	t := ui.NewTable()
	for _, b := range s.snapshot() {
		state := ui.Dim("offline")
		if b.Online {
			state = ui.Green("online")
		}
		t.Row(b.Name, b.Engine, state, fmt.Sprintf("(%d models)", len(b.Models)))
	}
	t.Render()
	return http.ListenAndServe(addr, mux)
}

// handleModels aggregates models across all backends plus aliases.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	var data []model
	seen := map[string]bool{}
	for _, b := range s.snapshot() {
		for _, m := range b.Models {
			if seen[m] {
				continue
			}
			seen[m] = true
			data = append(data, model{ID: m, Object: "model", OwnedBy: b.Name})
		}
	}
	for alias := range s.conf().Aliases {
		if !seen[alias] {
			data = append(data, model{ID: alias, Object: "model", OwnedBy: "alias"})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// handleBackends returns the discovered backends (root-privileged discovery).
func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.snapshot())
}

// handleChange installs or removes a snap via snapd (the daemon runs as root),
// streaming newline-delimited JSON progress events to the CLI, then refreshes.
func (s *Server) handleChange(w http.ResponseWriter, r *http.Request, op func(string, func(snapd.Progress)) error) {
	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	emit := func(v any) {
		enc.Encode(v)
		if flusher != nil {
			flusher.Flush()
		}
	}
	err := op(req.Name, func(p snapd.Progress) {
		emit(map[string]any{"progress": p})
	})
	if err != nil {
		emit(map[string]any{"error": err.Error()})
		return
	}
	s.refresh()
	emit(map[string]any{"status": "ok"})
}

// handleProxy routes a chat/completion/embedding request to its backend.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var probe struct {
		Model string `json:"model"`
	}
	json.Unmarshal(body, &probe)

	bs := s.snapshot()
	b, resolved := backend.FindForModel(bs, s.conf().Aliases, probe.Model)
	if b == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"message": fmt.Sprintf("model %q not found; see GET /v1/models", probe.Model),
				"type":    "model_not_found",
			},
		})
		return
	}

	// Rewrite the model field to the alias-resolved id the backend expects.
	if resolved != probe.Model {
		body = rewriteModel(body, resolved)
	}

	// Build the upstream URL: backend base + the suffix after /v1.
	suffix := r.URL.Path[len("/v1"):]
	target := b.BaseURL + suffix

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if b.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.APIKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Pass through status, content-type, and body (streaming-friendly).
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			break
		}
	}
}

// rewriteModel replaces the "model" field in a JSON body.
func rewriteModel(body []byte, model string) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	m["model"] = model
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// ---- CLI-side client for the daemon's management endpoints ----

// Backends fetches the daemon's root-privileged backend discovery.
func Backends(cfg *config.Config) ([]backend.Backend, bool) {
	url := fmt.Sprintf("http://%s:%d/v1/backends", cfg.Proxy.Bind, cfg.Proxy.Port)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, false
	}
	var bs []backend.Backend
	if json.NewDecoder(resp.Body).Decode(&bs) != nil {
		return nil, false
	}
	return bs, true
}

// RequestInstall asks the daemon to install a snap (it runs as root).
func RequestInstall(cfg *config.Config, name string, onProgress func(snapd.Progress)) error {
	return requestChange(cfg, "install", name, onProgress)
}

// RequestRemove asks the daemon to remove a snap.
func RequestRemove(cfg *config.Config, name string, onProgress func(snapd.Progress)) error {
	return requestChange(cfg, "remove", name, onProgress)
}

// requestChange streams progress events from the daemon's install/remove endpoint.
func requestChange(cfg *config.Config, op, name string, onProgress func(snapd.Progress)) error {
	url := fmt.Sprintf("http://%s:%d/v1/%s", cfg.Proxy.Bind, cfg.Proxy.Port, op)
	body, _ := json.Marshal(map[string]string{"name": name})
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var ev struct {
			Progress *snapd.Progress `json:"progress"`
			Status   string          `json:"status"`
			Error    string          `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Error != "":
			return fmt.Errorf("%s", ev.Error)
		case ev.Progress != nil && onProgress != nil:
			onProgress(*ev.Progress)
		case ev.Status == "ok":
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}

// IsUp probes a running proxy at the configured address.
func IsUp(cfg *config.Config) bool {
	url := fmt.Sprintf("http://%s:%d/healthz", cfg.Proxy.Bind, cfg.Proxy.Port)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}
