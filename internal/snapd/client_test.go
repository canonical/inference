package snapd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newUnixServer starts an httptest server listening on a Unix socket in a
// fresh temp directory and returns the socket path. The server is closed
// automatically at test cleanup.
func newUnixServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()

	// Unix socket paths are limited to roughly 108 bytes, so use a short
	// directory under /tmp rather than t.TempDir(), which nests deeply.
	dir, err := os.MkdirTemp("", "snapd-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "snapd.socket")

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on unix socket: %v", err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	return socket
}

func TestStatuses_SuccessfulEnvelope(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/snaps" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.URL.Query().Get("select") == "all" {
			t.Errorf("request must not use select=all, got query %q", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":[
			{"name":"gemma4","status":"active"},
			{"name":"qwen3","status":"installed"}
		]}`)
	})

	client := &Client{Sockets: []string{socket}}
	statuses, err := client.Statuses(context.Background())
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if statuses["gemma4"] != "active" || statuses["qwen3"] != "installed" {
		t.Fatalf("got %+v", statuses)
	}
}

func TestStatuses_APIErrorEnvelope(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"type":"error","status":"Internal Server Error","result":{"message":"boom"}}`)
	})

	client := &Client{Sockets: []string{socket}}
	if _, err := client.Statuses(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStatuses_NonOKStatus(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"type":"sync","status":"Forbidden","result":[]}`)
	})

	client := &Client{Sockets: []string{socket}}
	if _, err := client.Statuses(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStatuses_MalformedJSON(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{not json`)
	})

	client := &Client{Sockets: []string{socket}}
	if _, err := client.Statuses(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStatuses_MissingResult(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK"}`)
	})

	client := &Client{Sockets: []string{socket}}
	statuses, err := client.Statuses(context.Background())
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("got %+v, want empty map", statuses)
	}
}

func TestStatuses_DuplicateCurrentRecordsRejected(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":[
			{"name":"gemma4","status":"active"},
			{"name":"gemma4","status":"installed"}
		]}`)
	})

	client := &Client{Sockets: []string{socket}}
	if _, err := client.Statuses(context.Background()); err == nil {
		t.Fatal("expected error for duplicate entries, got nil")
	}
}

func TestStatuses_UnavailableSocket(t *testing.T) {
	client := &Client{Sockets: []string{filepath.Join(t.TempDir(), "does-not-exist.socket")}}
	if _, err := client.Statuses(context.Background()); err == nil {
		t.Fatal("expected error for unavailable socket, got nil")
	}
}

func TestStatuses_FallsBackToNextCandidateWhenFirstUnreachable(t *testing.T) {
	working := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":[{"name":"gemma4","status":"active"}]}`)
	})
	missing := filepath.Join(t.TempDir(), "missing.socket")

	client := &Client{Sockets: []string{missing, working}}
	statuses, err := client.Statuses(context.Background())
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if statuses["gemma4"] != "active" {
		t.Fatalf("got %+v", statuses)
	}
}

func TestStatuses_Timeout(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":[]}`)
	})

	client := &Client{
		Sockets: []string{socket},
		newClient: func(socket string) *http.Client {
			c := newHTTPClient(socket)
			c.Timeout = 10 * time.Millisecond
			return c
		},
	}
	if _, err := client.Statuses(context.Background()); err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestCandidateSockets_Host(t *testing.T) {
	t.Setenv("SNAP", "")
	os.Unsetenv("SNAP")
	got := CandidateSockets()
	want := []string{"/run/snapd.socket"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCandidateSockets_Confined(t *testing.T) {
	t.Setenv("SNAP", "/snap/inference/current")
	got := CandidateSockets()
	want := []string{"/run/snapd-snap.socket", "/run/snapd.socket"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// jsonRoundTrip is a sanity check that the envelope decodes as expected from
// raw JSON, guarding against accidental field-tag typos.
func TestSnapsEnvelope_Decode(t *testing.T) {
	var env snapsEnvelope
	if err := json.Unmarshal([]byte(`{"type":"sync","status":"OK","result":[{"name":"a","status":"active"}]}`), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Result) != 1 || env.Result[0].Name != "a" {
		t.Fatalf("got %+v", env)
	}
}
