// Package snapd provides a typed, read-only client for snapd's HTTP API
// over a Unix socket. It never shells out to the snap CLI, and it is used
// identically for host development and confined execution.
package snapd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// errUnreachable marks an error as a failure to reach a candidate socket at
// all (as opposed to a snapd-level API error), so the client can try the
// next candidate.
var errUnreachable = errors.New("snapd socket unreachable")

// Client is a typed, read-only snapd client used to list installed snaps.
// The zero value is not usable; construct one with NewClient.
type Client struct {
	// Sockets are the candidate Unix sockets to try, in order.
	Sockets []string

	// newClient builds an *http.Client for a socket path. Overridable in
	// tests; defaults to newHTTPClient.
	newClient func(socket string) *http.Client
}

// NewClient constructs a Client using the default socket candidates for the
// current execution context (host or confined).
func NewClient() *Client {
	return &Client{Sockets: CandidateSockets()}
}

// Statuses fetches the current installed snaps from snapd's read-only
// /v2/snaps endpoint (without select=all, so only current revisions are
// returned) and returns their statuses keyed by snap name. It implements
// providers.SnapStatusSource.
func (c *Client) Statuses(ctx context.Context) (map[string]string, error) {
	sockets := c.Sockets
	if len(sockets) == 0 {
		sockets = CandidateSockets()
	}
	newClient := c.newClient
	if newClient == nil {
		newClient = newHTTPClient
	}

	var unreachable []error
	for _, socket := range sockets {
		env, err := getSnaps(ctx, newClient(socket))
		if err != nil {
			if errors.Is(err, errUnreachable) {
				unreachable = append(unreachable, fmt.Errorf("%s: %w", socket, err))
				continue
			}
			return nil, err
		}
		return toStatuses(env)
	}
	return nil, fmt.Errorf("no snapd socket was reachable: %w", errors.Join(unreachable...))
}

// getSnaps performs the GET /v2/snaps request and decodes its envelope. It
// deliberately omits select=all: inactive historical revisions must not
// produce duplicate or misleading provider rows.
func getSnaps(ctx context.Context, client *http.Client) (snapsEnvelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/v2/snaps", nil)
	if err != nil {
		return snapsEnvelope{}, fmt.Errorf("building snapd request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return snapsEnvelope{}, fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer resp.Body.Close()

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

	if resp.StatusCode != http.StatusOK || env.Type == "error" {
		return snapsEnvelope{}, fmt.Errorf("snapd returned an error (HTTP %d, status %q)", resp.StatusCode, env.Status)
	}

	return env, nil
}

// toStatuses converts a decoded envelope into a name-to-status map,
// rejecting entries that would silently corrupt the result.
func toStatuses(env snapsEnvelope) (map[string]string, error) {
	statuses := make(map[string]string, len(env.Result))
	for _, s := range env.Result {
		if s.Name == "" {
			return nil, fmt.Errorf("snapd returned a snap entry with an empty name")
		}
		if _, dup := statuses[s.Name]; dup {
			return nil, fmt.Errorf("snapd returned duplicate entries for snap %q", s.Name)
		}
		statuses[s.Name] = s.Status
	}
	return statuses, nil
}
