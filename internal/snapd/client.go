package snapd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"time"
)

var errUnreachable = errors.New("snapd socket unreachable")

var ErrAccessDenied = errors.New("snapd socket denied access")

var ErrAlreadyInstalled = errors.New("snap is already installed")

var ErrNotInstalled = errors.New("snap is not installed")

var ErrChangeConflict = errors.New("snap has a conflicting change in progress")

const snapAlreadyInstalledKind = "snap-already-installed"

const snapNotInstalledKind = "snap-not-installed"

const snapChangeConflictKind = "snap-change-conflict"

const maxResponseBytes = 4 << 20

type Client struct {
	Sockets   []string
	newClient func(socket string) *http.Client
}

func NewClient() *Client {
	return &Client{Sockets: CandidateSockets()}
}

func (c *Client) Statuses(ctx context.Context) (map[string]string, error) {
	sockets := c.Sockets
	if len(sockets) == 0 {
		sockets = CandidateSockets()
	}
	newClient := c.newClient
	if newClient == nil {
		newClient = newHTTPClient
	}

	var failures []error
	for _, socket := range sockets {
		snaps, err := getSnaps(ctx, newClient(socket))
		if err != nil {
			if errors.Is(err, errUnreachable) || errors.Is(err, ErrAccessDenied) {
				failures = append(failures, err)
				continue
			}
			return nil, err
		}
		statuses := make(map[string]string, len(snaps))
		for _, snap := range snaps {
			if snap.Name == "" {
				return nil, fmt.Errorf("snapd returned a snap with no name")
			}
			if _, found := statuses[snap.Name]; found {
				return nil, fmt.Errorf("snapd returned duplicate entries for %q", snap.Name)
			}
			statuses[snap.Name] = snap.Status
		}
		return statuses, nil
	}
	return nil, fmt.Errorf("no snapd socket granted access: %w", errors.Join(failures...))
}

func (c *Client) Install(ctx context.Context, name string) (changeID string, err error) {
	return c.snapAction(ctx, name, "install")
}

func (c *Client) Remove(ctx context.Context, name string) (changeID string, err error) {
	return c.snapAction(ctx, name, "remove")
}

func (c *Client) snapAction(ctx context.Context, name, action string) (string, error) {
	sockets := c.Sockets
	if len(sockets) == 0 {
		sockets = CandidateSockets()
	}
	newClient := c.newClient
	if newClient == nil {
		newClient = newHTTPClient
	}

	var failures []error
	for _, socket := range sockets {
		changeID, err := performSnapAction(ctx, newClient(socket), name, action)
		if err == nil {
			return changeID, nil
		}
		if errors.Is(err, errUnreachable) || errors.Is(err, ErrAccessDenied) {
			failures = append(failures, err)
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("no snapd socket granted access: %w", errors.Join(failures...))
}

func performSnapAction(ctx context.Context, client *http.Client, name, action string) (string, error) {
	reqBody, err := json.Marshal(map[string]string{"action": action})
	if err != nil {
		return "", fmt.Errorf("encoding snapd %s request: %w", action, err)
	}

	url := fmt.Sprintf("http://localhost/v2/snaps/%s", name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("building snapd request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer resp.Body.Close()

	env, err := decodeEnvelope(resp)
	if err != nil {
		return "", err
	}

	switch env.Type {
	case "async":
		if env.Change == "" {
			return "", fmt.Errorf("snapd accepted the %s request but returned no change id", action)
		}
		return env.Change, nil
	case "sync":
		return "", nil
	default:
		return "", fmt.Errorf("snapd returned unexpected response type %q", env.Type)
	}
}

func (c *Client) Change(ctx context.Context, changeID string) (Change, error) {
	sockets := c.Sockets
	if len(sockets) == 0 {
		sockets = CandidateSockets()
	}
	newClient := c.newClient
	if newClient == nil {
		newClient = newHTTPClient
	}

	var failures []error
	for _, socket := range sockets {
		change, err := getChange(ctx, newClient(socket), changeID)
		if err == nil {
			return change, nil
		}
		if errors.Is(err, errUnreachable) || errors.Is(err, ErrAccessDenied) {
			failures = append(failures, err)
			continue
		}
		return Change{}, err
	}
	return Change{}, fmt.Errorf("no snapd socket granted access: %w", errors.Join(failures...))
}

func (c *Client) Abort(ctx context.Context, changeID string) error {
	sockets := c.Sockets
	if len(sockets) == 0 {
		sockets = CandidateSockets()
	}
	newClient := c.newClient
	if newClient == nil {
		newClient = newHTTPClient
	}

	var failures []error
	for _, socket := range sockets {
		err := abortChange(ctx, newClient(socket), changeID)
		if err == nil {
			return nil
		}
		if errors.Is(err, errUnreachable) || errors.Is(err, ErrAccessDenied) {
			failures = append(failures, err)
			continue
		}
		return err
	}
	return fmt.Errorf("no snapd socket granted access: %w", errors.Join(failures...))
}

func abortChange(ctx context.Context, client *http.Client, changeID string) error {
	reqBody, err := json.Marshal(map[string]string{"action": "abort"})
	if err != nil {
		return fmt.Errorf("encoding snapd abort request: %w", err)
	}

	url := fmt.Sprintf("http://localhost/v2/changes/%s", changeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("building snapd request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer resp.Body.Close()

	env, err := decodeEnvelope(resp)
	if err != nil {
		return err
	}
	if env.Type != "sync" {
		return fmt.Errorf("snapd returned unexpected response type %q", env.Type)
	}
	return nil
}

func getChange(ctx context.Context, client *http.Client, changeID string) (Change, error) {
	url := fmt.Sprintf("http://localhost/v2/changes/%s", changeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Change{}, fmt.Errorf("building snapd request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Change{}, fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer resp.Body.Close()

	env, err := decodeEnvelope(resp)
	if err != nil {
		return Change{}, err
	}

	var change Change
	if err := json.Unmarshal(env.Result, &change); err != nil {
		return Change{}, fmt.Errorf("decoding snapd change: %w", err)
	}
	return change, nil
}

func (c *Client) ChangesInProgress(ctx context.Context, name string) ([]Change, error) {
	sockets := c.Sockets
	if len(sockets) == 0 {
		sockets = CandidateSockets()
	}
	newClient := c.newClient
	if newClient == nil {
		newClient = newHTTPClient
	}

	var failures []error
	for _, socket := range sockets {
		changes, err := getChangesForSnap(ctx, newClient(socket), name)
		if err == nil {
			return changes, nil
		}
		if errors.Is(err, errUnreachable) || errors.Is(err, ErrAccessDenied) {
			failures = append(failures, err)
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("no snapd socket granted access: %w", errors.Join(failures...))
}

func getChangesForSnap(ctx context.Context, client *http.Client, name string) ([]Change, error) {
	url := fmt.Sprintf("http://localhost/v2/changes?select=in-progress&for=%s", neturl.QueryEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building snapd request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer resp.Body.Close()

	env, err := decodeEnvelope(resp)
	if err != nil {
		return nil, err
	}

	var changes []Change
	if len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, &changes); err != nil {
			return nil, fmt.Errorf("decoding snapd change list: %w", err)
		}
	}
	return changes, nil
}

func decodeEnvelope(resp *http.Response) (snapsEnvelope, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return snapsEnvelope{}, fmt.Errorf("reading snapd response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return snapsEnvelope{}, fmt.Errorf("snapd response exceeded %d bytes", maxResponseBytes)
	}

	var env snapsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return snapsEnvelope{}, fmt.Errorf("decoding snapd response (HTTP %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode >= 300 || env.Type == "error" {
		var result errorResult
		if len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, &result); err != nil {
				return snapsEnvelope{}, fmt.Errorf("decoding snapd error: %w", err)
			}
		}
		if result.Kind == snapAlreadyInstalledKind {
			return snapsEnvelope{}, ErrAlreadyInstalled
		}
		if result.Kind == snapNotInstalledKind {
			return snapsEnvelope{}, ErrNotInstalled
		}
		if result.Kind == snapChangeConflictKind {
			return snapsEnvelope{}, ErrChangeConflict
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			if result.Message != "" {
				return snapsEnvelope{}, fmt.Errorf("%w: snapd returned HTTP %d: %s", ErrAccessDenied, resp.StatusCode, result.Message)
			}
			return snapsEnvelope{}, fmt.Errorf("%w: snapd returned HTTP %d (%s)", ErrAccessDenied, resp.StatusCode, env.Status)
		}
		if result.Message != "" {
			return snapsEnvelope{}, fmt.Errorf("snapd returned HTTP %d: %s", resp.StatusCode, result.Message)
		}
		return snapsEnvelope{}, fmt.Errorf("snapd returned HTTP %d (%s)", resp.StatusCode, env.Status)
	}

	return env, nil
}

func getSnaps(ctx context.Context, client *http.Client) ([]snapInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/v2/snaps", nil)
	if err != nil {
		return nil, fmt.Errorf("building snapd request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer resp.Body.Close()

	env, err := decodeEnvelope(resp)
	if err != nil {
		return nil, err
	}
	if env.Type != "sync" {
		return nil, fmt.Errorf("snapd returned unexpected response type %q", env.Type)
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return nil, fmt.Errorf("snapd response omitted the result")
	}

	var snaps []snapInfo
	if err := json.Unmarshal(env.Result, &snaps); err != nil {
		return nil, fmt.Errorf("decoding snapd snap list: %w", err)
	}
	return snaps, nil
}

func CandidateSockets() []string {
	if os.Getenv("SNAP") != "" && os.Getenv("SNAP_NAME") != "go" {
		return []string{"/run/snapd-snap.socket", "/run/snapd.socket"}
	}
	return []string{"/run/snapd.socket"}
}

func newHTTPClient(socket string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
			},
		},
	}
}

type snapsEnvelope struct {
	Type   string          `json:"type"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Change string          `json:"change"`
}

type errorResult struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

type snapInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Change struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Ready   bool   `json:"ready"`
	Err     string `json:"err"`
	Tasks   []Task `json:"tasks"`
}

type Task struct {
	Summary  string       `json:"summary"`
	Status   string       `json:"status"`
	Progress TaskProgress `json:"progress"`
}

type TaskProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}
