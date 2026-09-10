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

var (
	ErrAccessDenied      = errors.New("snapd socket denied access")
	ErrSocketUnreachable = errors.New("cannot reach snapd socket")
	ErrAlreadyInstalled  = errors.New("snap is already installed")
	ErrNotInstalled      = errors.New("snap is not installed")
	ErrChangeConflict    = errors.New("snap has a conflicting change in progress")
	ErrTransient         = errors.New("temporary snapd communication failure")
)

const (
	snapAlreadyInstalledKind = "snap-already-installed"
	snapNotInstalledKind     = "snap-not-installed"
	snapChangeConflictKind   = "snap-change-conflict"
	snapNotFoundKind         = "snap-not-found"
)

const (
	// StatusNotInstalled is returned by Client.Status when a snap is not
	// installed. Unlike SnapStatusActive and SnapStatusInstalled, snapd itself
	// has no such value: it simply omits the snap from its API responses.
	StatusNotInstalled  = "not installed"
	SnapStatusActive    = "active"
	SnapStatusInstalled = "installed"
)

const (
	maxResponseBytes = 4 << 20

	DefaultSocketPath = "/run/snapd.socket"
	EnvVar            = "SNAPD_SOCKET"
)

type Client struct {
	Socket    string
	newClient func(socket string) *http.Client
}

func NewClient() *Client {
	return &Client{Socket: DefaultSocket()}
}

func (c *Client) Status(ctx context.Context, name string) (string, error) {
	snap, err := getSnap(ctx, c.httpClient(), name)
	if err != nil {
		if errors.Is(err, ErrNotInstalled) {
			return StatusNotInstalled, nil
		}
		return "", err
	}
	switch snap.Status {
	case SnapStatusActive, SnapStatusInstalled:
		return snap.Status, nil
	default:
		return "", fmt.Errorf("snapd returned unexpected status %q for %q", snap.Status, name)
	}
}

func (c *Client) httpClient() *http.Client {
	socket := c.Socket
	if socket == "" {
		socket = DefaultSocket()
	}
	if c.newClient != nil {
		return c.newClient(socket)
	}
	return newHTTPClient(socket)
}

func (c *Client) Install(ctx context.Context, name string) (changeID string, err error) {
	return c.snapAction(ctx, name, "install")
}

func (c *Client) Remove(ctx context.Context, name string) (changeID string, err error) {
	return c.snapAction(ctx, name, "remove")
}

func (c *Client) snapAction(ctx context.Context, name, action string) (string, error) {
	return performSnapAction(ctx, c.httpClient(), name, action)
}

func performSnapAction(ctx context.Context, client *http.Client, name, action string) (string, error) {
	reqBody, err := json.Marshal(map[string]string{"action": action})
	if err != nil {
		return "", fmt.Errorf("encoding snapd %s request: %w", action, err)
	}

	url := fmt.Sprintf("http://localhost/v2/snaps/%s", neturl.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("building snapd request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", wrapCallErr(err)
	}
	defer resp.Body.Close()

	env, err := decodeEnvelope(resp)
	if err != nil {
		return "", err
	}

	switch env.Type {
	case "async":
		if resp.StatusCode != http.StatusAccepted {
			return "", fmt.Errorf("snapd returned an async %s response with HTTP status %d", action, resp.StatusCode)
		}
		if env.Change == "" {
			return "", fmt.Errorf("snapd accepted the %s request but returned no change id", action)
		}
		return env.Change, nil
	case "sync":
		return "", fmt.Errorf("snapd returned an unexpected synchronous response for %s", action)
	default:
		return "", fmt.Errorf("snapd returned unexpected response type %q", env.Type)
	}
}

func (c *Client) Change(ctx context.Context, changeID string) (Change, error) {
	return getChange(ctx, c.httpClient(), changeID)
}

func (c *Client) Abort(ctx context.Context, changeID string) error {
	return abortChange(ctx, c.httpClient(), changeID)
}

func abortChange(ctx context.Context, client *http.Client, changeID string) error {
	reqBody, err := json.Marshal(map[string]string{"action": "abort"})
	if err != nil {
		return fmt.Errorf("encoding snapd abort request: %w", err)
	}

	url := fmt.Sprintf("http://localhost/v2/changes/%s", neturl.PathEscape(changeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("building snapd request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return wrapCallErr(err)
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
	url := fmt.Sprintf("http://localhost/v2/changes/%s", neturl.PathEscape(changeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Change{}, fmt.Errorf("building snapd request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return Change{}, wrapCallErr(err)
		}
		return Change{}, fmt.Errorf("%w: calling snapd: %w", ErrTransient, err)
	}
	defer resp.Body.Close()

	env, err := decodeEnvelope(resp)
	if err != nil {
		return Change{}, err
	}
	if env.Type != "sync" {
		return Change{}, fmt.Errorf("snapd returned unexpected response type %q", env.Type)
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return Change{}, fmt.Errorf("snapd response omitted the result")
	}

	var change Change
	if err := json.Unmarshal(env.Result, &change); err != nil {
		return Change{}, fmt.Errorf("decoding snapd change: %w", err)
	}
	change.Maintenance = env.Maintenance
	return change, nil
}

func (c *Client) ChangesInProgress(ctx context.Context, name string) ([]Change, error) {
	return getChangesForSnap(ctx, c.httpClient(), name)
}

func getChangesForSnap(ctx context.Context, client *http.Client, name string) ([]Change, error) {
	url := fmt.Sprintf("http://localhost/v2/changes?select=in-progress&for=%s", neturl.QueryEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building snapd request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, wrapCallErr(err)
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

	var changes []Change
	if err := json.Unmarshal(env.Result, &changes); err != nil {
		return nil, fmt.Errorf("decoding snapd change list: %w", err)
	}
	return changes, nil
}

func decodeEnvelope(resp *http.Response) (envelope, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return envelope{}, fmt.Errorf("reading snapd response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return envelope{}, fmt.Errorf("snapd response exceeded %d bytes", maxResponseBytes)
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return envelope{}, fmt.Errorf("decoding snapd response (HTTP %d): %w", resp.StatusCode, err)
	}
	if env.StatusCode != 0 && env.StatusCode != resp.StatusCode {
		return envelope{}, fmt.Errorf(
			"snapd response status-code %d does not match HTTP status %d",
			env.StatusCode,
			resp.StatusCode,
		)
	}

	if resp.StatusCode >= 300 || env.Type == "error" {
		var result errorResult
		if len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, &result); err != nil {
				return envelope{}, fmt.Errorf("decoding snapd error: %w", err)
			}
		}
		if result.Kind == snapAlreadyInstalledKind {
			return envelope{}, withMessage(ErrAlreadyInstalled, result.Message)
		}
		if result.Kind == snapNotInstalledKind || result.Kind == snapNotFoundKind {
			return envelope{}, withMessage(ErrNotInstalled, result.Message)
		}
		if result.Kind == snapChangeConflictKind {
			return envelope{}, withMessage(ErrChangeConflict, result.Message)
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			if result.Message != "" {
				return envelope{}, fmt.Errorf("%w: snapd returned HTTP %d: %s", ErrAccessDenied, resp.StatusCode, result.Message)
			}
			return envelope{}, fmt.Errorf("%w: snapd returned HTTP %d (%s)", ErrAccessDenied, resp.StatusCode, env.Status)
		}
		if resp.StatusCode >= http.StatusInternalServerError {
			if result.Message != "" {
				return envelope{}, fmt.Errorf("%w: snapd returned HTTP %d: %s", ErrTransient, resp.StatusCode, result.Message)
			}
			return envelope{}, fmt.Errorf("%w: snapd returned HTTP %d (%s)", ErrTransient, resp.StatusCode, env.Status)
		}
		if result.Message != "" {
			return envelope{}, fmt.Errorf("snapd returned HTTP %d: %s", resp.StatusCode, result.Message)
		}
		return envelope{}, fmt.Errorf("snapd returned HTTP %d (%s)", resp.StatusCode, env.Status)
	}

	return env, nil
}

// wrapCallErr classifies failures to reach the snapd socket itself (as
// opposed to an error response from snapd). Confinement blocks the socket
// connection with a permission error when the snapd-control interface is
// not connected, which otherwise surfaces as an opaque dial error.
func wrapCallErr(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w: %w", ErrSocketUnreachable, err)
	}
	return fmt.Errorf("calling snapd: %w", err)
}

// Keep snapd's message while preserving errors.Is support.
func withMessage(sentinel error, message string) error {
	if message == "" {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, message)
}

func getSnap(ctx context.Context, client *http.Client, name string) (snapInfo, error) {
	url := fmt.Sprintf("http://localhost/v2/snaps/%s", neturl.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return snapInfo{}, fmt.Errorf("building snapd request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return snapInfo{}, wrapCallErr(err)
	}
	defer resp.Body.Close()

	env, err := decodeEnvelope(resp)
	if err != nil {
		return snapInfo{}, err
	}
	if env.Type != "sync" {
		return snapInfo{}, fmt.Errorf("snapd returned unexpected response type %q", env.Type)
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return snapInfo{}, fmt.Errorf("snapd response omitted the result")
	}

	var snap snapInfo
	if err := json.Unmarshal(env.Result, &snap); err != nil {
		return snapInfo{}, fmt.Errorf("decoding snapd snap: %w", err)
	}
	return snap, nil
}

func DefaultSocket() string {
	if socket := os.Getenv(EnvVar); socket != "" {
		return socket
	}
	return DefaultSocketPath
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

type envelope struct {
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	StatusCode  int             `json:"status-code"`
	Result      json.RawMessage `json:"result"`
	Change      string          `json:"change"`
	Maintenance *Maintenance    `json:"maintenance"`
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
	Status      string       `json:"status"`
	Summary     string       `json:"summary"`
	Ready       bool         `json:"ready"`
	Err         string       `json:"err"`
	Tasks       []Task       `json:"tasks"`
	Maintenance *Maintenance `json:"-"`
}

type Maintenance struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type Task struct {
	ID        string       `json:"id"`
	Kind      string       `json:"kind"`
	Summary   string       `json:"summary"`
	Status    string       `json:"status"`
	Log       []string     `json:"log"`
	Progress  TaskProgress `json:"progress"`
	SpawnTime time.Time    `json:"spawn-time"`
	ReadyTime time.Time    `json:"ready-time"`
}

type TaskProgress struct {
	Label string `json:"label"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}
