package snapd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

var errUnreachable = errors.New("snapd socket unreachable")
var errAccessDenied = errors.New("snapd socket denied access")

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
			if errors.Is(err, errUnreachable) || errors.Is(err, errAccessDenied) {
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading snapd response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("snapd response exceeded %d bytes", maxResponseBytes)
	}

	var env snapsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decoding snapd response (HTTP %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK || env.Type == "error" {
		var result errorResult
		if len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, &result); err != nil {
				return nil, fmt.Errorf("decoding snapd error: %w", err)
			}
		}
		if resp.StatusCode == http.StatusForbidden {
			if result.Message != "" {
				return nil, fmt.Errorf("%w: snapd returned HTTP 403: %s", errAccessDenied, result.Message)
			}
			return nil, fmt.Errorf("%w: snapd returned HTTP 403 (%s)", errAccessDenied, env.Status)
		}
		if result.Message != "" {
			return nil, fmt.Errorf("snapd returned HTTP %d: %s", resp.StatusCode, result.Message)
		}
		return nil, fmt.Errorf("snapd returned HTTP %d (%s)", resp.StatusCode, env.Status)
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
}

type errorResult struct {
	Message string `json:"message"`
}

type snapInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}
