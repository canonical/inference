package snapd

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"
)

const (
	dialTimeout      = 5 * time.Second
	requestTimeout   = 10 * time.Second
	maxResponseBytes = 4 << 20 // 4 MiB
)

// CandidateSockets returns the snapd Unix sockets to try, in priority order,
// for the current execution context.
//
// Confined execution reaches snapd over /run/snapd-snap.socket, where snapd
// authorizes the request by the calling snap's connected interfaces
// (snapd-control). /run/snapd.socket is considered next only where the
// connected interface and snapd authorization permit it. Host development
// execution uses /run/snapd.socket directly.
func CandidateSockets() []string {
	if inSnap() {
		return []string{"/run/snapd-snap.socket", "/run/snapd.socket"}
	}
	return []string{"/run/snapd.socket"}
}

func inSnap() bool { return os.Getenv("SNAP") != "" }

// newHTTPClient returns an *http.Client that dials the given Unix socket
// path, with a bounded connection timeout and a bounded overall request
// timeout.
func newHTTPClient(socket string) *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: dialTimeout}
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
}
