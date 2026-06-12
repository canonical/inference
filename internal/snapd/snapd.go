// Package snapd manages other snaps (reading config, installing) two ways:
//   - inside the confined snap ($SNAP set): via the snapd REST API. Requests go
//     over /run/snapd-snap.socket, where snapd authorizes by the calling snap's
//     connected interfaces (snapd-control) rather than by uid;
//   - as a plain host binary (development): via the `snap` CLI.
package snapd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// inSnap reports whether we're running inside the confined snap.
func inSnap() bool { return os.Getenv("SNAP") != "" }

// candidateSockets lists snapd sockets to try, in order. Running as root (the
// proxy daemon) we use the privileged socket directly — uid 0 satisfies snapd's
// authorization, and snapd-control grants the AppArmor access to reach it.
// Confined non-root callers can't manage snaps over either socket (snapd
// authorizes management by uid/polkit, not by interface), so they delegate to
// the daemon instead of calling this.
func candidateSockets() []string {
	if os.Geteuid() == 0 {
		return []string{"/run/snapd.socket"}
	}
	if inSnap() {
		return []string{"/run/snapd-snap.socket", "/run/snapd.socket"}
	}
	return []string{"/run/snapd.socket"}
}

// Conf returns a snap's configuration document (same shape as `snap get -d`),
// or ok=false if unavailable.
func Conf(name string) ([]byte, bool) {
	if !inSnap() {
		out, err := exec.Command("snap", "get", "-d", name).Output()
		if err != nil {
			return nil, false
		}
		return out, true
	}
	for _, sock := range candidateSockets() {
		body, status, err := socketGet(sock, "/v2/snaps/"+name+"/conf")
		if err != nil {
			debugf("conf %s via %s: %v", name, sock, err)
			continue
		}
		if status != 200 {
			debugf("conf %s via %s: HTTP %d: %s", name, sock, status, snippet(body))
			continue
		}
		var env struct {
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(body, &env) == nil && len(env.Result) > 0 {
			return env.Result, true
		}
		debugf("conf %s via %s: empty result: %s", name, sock, snippet(body))
	}
	return nil, false
}

// Progress is a snapshot of an in-flight snapd change.
type Progress struct {
	Status     string `json:"status"`      // Doing | Done | Error …
	Summary    string `json:"summary"`     // current task, e.g. Download snap "gemma4"
	Done       int64  `json:"done"`        // current task progress
	Total      int64  `json:"total"`       // current task size
	TasksDone  int    `json:"tasks_done"`  // completed tasks
	TasksTotal int    `json:"tasks_total"` // total tasks
	Err        string `json:"err,omitempty"`
}

// Installed returns the names of installed snaps. The /v2/snaps list endpoint is
// open (no privilege needed); in development it shells out to `snap list`.
func Installed() []string {
	if !inSnap() {
		out, err := exec.Command("snap", "list").Output()
		if err != nil {
			return nil
		}
		var names []string
		for i, line := range strings.Split(string(out), "\n") {
			if i == 0 || line == "" {
				continue // header / blank
			}
			if f := strings.Fields(line); len(f) > 0 {
				names = append(names, f[0])
			}
		}
		return names
	}
	for _, sock := range candidateSockets() {
		body, status, err := socketGet(sock, "/v2/snaps")
		if err != nil || status != 200 {
			continue
		}
		var env struct {
			Result []struct {
				Name string `json:"name"`
			} `json:"result"`
		}
		if json.Unmarshal(body, &env) == nil {
			var names []string
			for _, s := range env.Result {
				names = append(names, s.Name)
			}
			return names
		}
	}
	return nil
}

// Connections reports which of the inference snap's plugs are connected,
// keyed by plug name (e.g. "snapd-control" -> true). ok=false if it can't be
// determined (e.g. the snap isn't installed / snapd is unreachable).
func Connections() (map[string]bool, bool) {
	if !inSnap() {
		out, err := exec.Command("snap", "connections", "inference").Output()
		if err != nil {
			return nil, false
		}
		conns := map[string]bool{}
		for i, line := range strings.Split(string(out), "\n") {
			if i == 0 || strings.TrimSpace(line) == "" {
				continue // header / blank
			}
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			// columns: Interface  Plug  Slot  [Notes]
			plug := strings.TrimPrefix(f[1], "inference:")
			conns[plug] = f[2] != "-" // connected when a slot is bound
		}
		return conns, true
	}
	for _, sock := range candidateSockets() {
		body, status, err := socketGet(sock, "/v2/connections?snap=inference")
		if err != nil || status != 200 {
			continue
		}
		var env struct {
			Result struct {
				Plugs []struct {
					Plug        string `json:"plug"`
					Connections []any  `json:"connections"`
				} `json:"plugs"`
			} `json:"result"`
		}
		if json.Unmarshal(body, &env) != nil {
			continue
		}
		conns := map[string]bool{}
		for _, p := range env.Result.Plugs {
			conns[p.Plug] = len(p.Connections) > 0
		}
		return conns, true
	}
	return nil, false
}

// Install installs a snap, blocking until done. onProgress (may be nil) is
// called repeatedly with the change status while it runs.
func Install(name string, onProgress func(Progress)) error {
	return change(name, "install", onProgress)
}

// Remove uninstalls a snap, blocking until done.
func Remove(name string, onProgress func(Progress)) error {
	return change(name, "remove", onProgress)
}

func change(name, op string, onProgress func(Progress)) error {
	if !inSnap() {
		// Development: the `snap` CLI prints its own progress bar.
		cmd := exec.Command("sudo", "snap", op, name)
		cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
		return cmd.Run()
	}
	payload, _ := json.Marshal(map[string]string{"action": op})
	var lastErr error
	for _, sock := range candidateSockets() {
		body, status, err := socketPost(sock, "/v2/snaps/"+name, payload)
		if err != nil {
			lastErr = err
			debugf("%s %s via %s: %v", op, name, sock, err)
			continue
		}
		var env struct {
			Type   string `json:"type"`
			Change string `json:"change"`
			Result struct {
				Message string `json:"message"`
			} `json:"result"`
		}
		json.Unmarshal(body, &env)
		debugf("%s %s via %s: HTTP %d type=%s change=%s", op, name, sock, status, env.Type, env.Change)
		if status >= 400 || env.Type == "error" {
			lastErr = fmt.Errorf("snapd: %s", env.Result.Message)
			continue
		}
		if env.Change == "" {
			lastErr = fmt.Errorf("snapd: no change id returned")
			continue
		}
		return pollChange(sock, env.Change, onProgress)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no snapd socket accepted the %s", op)
	}
	return lastErr
}

// pollChange polls a snapd change until ready, reporting progress.
func pollChange(sock, id string, onProgress func(Progress)) error {
	for {
		p, ready, err := changeProgress(sock, id)
		if err != nil {
			return err
		}
		if onProgress != nil {
			onProgress(p)
		}
		if ready {
			if p.Status == "Error" {
				if p.Err == "" {
					p.Err = "install failed"
				}
				return fmt.Errorf("snapd: %s", p.Err)
			}
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// changeProgress fetches a change and summarizes its current task.
func changeProgress(sock, id string) (Progress, bool, error) {
	body, status, err := socketGet(sock, "/v2/changes/"+id)
	if err != nil {
		return Progress{}, false, err
	}
	if status != 200 {
		return Progress{}, false, fmt.Errorf("snapd change %s: HTTP %d", id, status)
	}
	var env struct {
		Result struct {
			Status string `json:"status"`
			Ready  bool   `json:"ready"`
			Err    string `json:"err"`
			Tasks  []struct {
				Status   string `json:"status"`
				Summary  string `json:"summary"`
				Progress struct {
					Done  int64 `json:"done"`
					Total int64 `json:"total"`
				} `json:"progress"`
			} `json:"tasks"`
		} `json:"result"`
	}
	json.Unmarshal(body, &env)
	r := env.Result
	p := Progress{Status: r.Status, Err: r.Err, TasksTotal: len(r.Tasks)}
	for _, t := range r.Tasks {
		if t.Status == "Done" {
			p.TasksDone++
		}
		if t.Status == "Doing" && p.Summary == "" {
			p.Summary = t.Summary
			p.Done, p.Total = t.Progress.Done, t.Progress.Total
		}
	}
	if p.Summary == "" && p.TasksTotal > 0 {
		p.Summary = fmt.Sprintf("%d/%d tasks", p.TasksDone, p.TasksTotal)
	}
	return p, r.Ready, nil
}

// ---- low-level unix-socket HTTP ----

func clientFor(sock string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute, // installs can be long
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
}

func socketGet(sock, path string) ([]byte, int, error) {
	resp, err := clientFor(sock).Get("http://localhost" + path)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

func socketPost(sock, path string, payload []byte) ([]byte, int, error) {
	resp, err := clientFor(sock).Post("http://localhost"+path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

// debugf logs to stderr when INFERENCE_DEBUG is set.
func debugf(format string, a ...any) {
	if os.Getenv("INFERENCE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[snapd] "+format+"\n", a...)
	}
}

func snippet(b []byte) string {
	const n = 200
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
