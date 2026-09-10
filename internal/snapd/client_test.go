package snapd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newUnixServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()

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

func writeAsyncAccepted(w http.ResponseWriter, changeID string) {
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"type":"async","status":"Accepted","status-code":202,"change":%q}`, changeID)
}

func TestStatus_ActiveSnapIsEnabled(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/snaps/gemma4" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"name":"gemma4","status":"active"}}`)
	})

	client := &Client{Socket: socket}
	status, err := client.Status(context.Background(), "gemma4")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != SnapStatusActive {
		t.Fatalf("got %q, want %q", status, SnapStatusActive)
	}
}

func TestStatus_InstalledSnapIsDisabled(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"name":"qwen3","status":"installed"}}`)
	})

	client := &Client{Socket: socket}
	status, err := client.Status(context.Background(), "qwen3")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != SnapStatusInstalled {
		t.Fatalf("got %q, want %q", status, SnapStatusInstalled)
	}
}

func TestStatus_SnapNotFoundIsNotInstalled(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"type":"error","status":"Not Found","status-code":404,"result":{
			"message":"snap \"smollm2\" not found",
			"kind":"snap-not-found"
		}}`)
	})

	client := &Client{Socket: socket}
	status, err := client.Status(context.Background(), "smollm2")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusNotInstalled {
		t.Fatalf("got %q, want %q", status, StatusNotInstalled)
	}
}

func TestStatus_APIErrorEnvelope(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"type":"error","status":"Internal Server Error","result":{"message":"boom"}}`)
	})

	client := &Client{Socket: socket}
	_, err := client.Status(context.Background(), "gemma4")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("API error was masked by result decoding: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected contextual snapd API error, got %v", err)
	}
}

func TestStatus_NonOKStatus(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"type":"sync","status":"Forbidden","result":{}}`)
	})

	client := &Client{Socket: socket}
	if _, err := client.Status(context.Background(), "gemma4"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStatus_ForbiddenErrorObjectIsNotDecodedAsSnap(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"type":"error","status":"Forbidden","result":{"message":"access denied"}}`)
	})

	client := &Client{Socket: socket}
	_, err := client.Status(context.Background(), "gemma4")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("API error was masked by result decoding: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected contextual permission error, got %v", err)
	}
}

func TestStatus_MalformedJSON(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{not json`)
	})

	client := &Client{Socket: socket}
	if _, err := client.Status(context.Background(), "gemma4"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStatus_MissingResult(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK"}`)
	})

	client := &Client{Socket: socket}
	if _, err := client.Status(context.Background(), "gemma4"); err == nil {
		t.Fatal("expected error for missing result, got nil")
	}
}

func TestStatus_UnexpectedStatusValue(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"name":"gemma4","status":"removed"}}`)
	})

	client := &Client{Socket: socket}
	if _, err := client.Status(context.Background(), "gemma4"); err == nil {
		t.Fatal("expected error for unexpected status value, got nil")
	}
}

func TestStatus_UnavailableSocket(t *testing.T) {
	client := &Client{Socket: filepath.Join(t.TempDir(), "does-not-exist.socket")}
	if _, err := client.Status(context.Background(), "gemma4"); err == nil {
		t.Fatal("expected error for unavailable socket, got nil")
	}
}

func TestStatus_Forbidden(t *testing.T) {
	forbidden := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"type":"error","status":"Forbidden","result":{"message":"access denied"}}`)
	})

	client := &Client{Socket: forbidden}
	_, err := client.Status(context.Background(), "gemma4")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected access denied error, got %v", err)
	}
}

func TestStatus_Timeout(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"name":"gemma4","status":"active"}}`)
	})

	client := &Client{
		Socket: socket,
		newClient: func(socket string) *http.Client {
			c := newHTTPClient(socket)
			c.Timeout = 10 * time.Millisecond
			return c
		},
	}
	if _, err := client.Status(context.Background(), "gemma4"); err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestDefaultSocketUsesSnapSocket(t *testing.T) {
	t.Setenv(EnvVar, "")
	if got := DefaultSocket(); got != DefaultSocketPath {
		t.Fatalf("got %q, want %q", got, DefaultSocketPath)
	}
}

func TestDefaultSocketPrefersEnvVar(t *testing.T) {
	t.Setenv(EnvVar, "/run/snapd.socket")
	if got := DefaultSocket(); got != "/run/snapd.socket" {
		t.Fatalf("got %q, want %q", got, "/run/snapd.socket")
	}
}

func TestInstall_AsyncResponseReturnsChangeID(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/snaps/smollm2" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		writeAsyncAccepted(w, "42")
	})

	client := &Client{Socket: socket}
	changeID, err := client.Install(context.Background(), "smollm2")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if changeID != "42" {
		t.Fatalf("got change id %q, want %q", changeID, "42")
	}
}

func TestInstall_AsyncResponseMissingChangeIDIsRejected(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeAsyncAccepted(w, "")
	})

	client := &Client{Socket: socket}
	if _, err := client.Install(context.Background(), "smollm2"); err == nil {
		t.Fatal("expected an error for a missing change id, got nil")
	}
}

func TestInstall_SyncResponseIsRejected(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
	})

	client := &Client{Socket: socket}
	if _, err := client.Install(context.Background(), "smollm2"); err == nil {
		t.Fatal("expected a synchronous action response to be rejected")
	}
}

func TestInstall_AsyncHTTPStatusIsRequired(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"async","status":"OK","status-code":200,"change":"42"}`)
	})

	client := &Client{Socket: socket}
	if _, err := client.Install(context.Background(), "smollm2"); err == nil {
		t.Fatal("expected an async response with HTTP 200 to be rejected")
	}
}

func TestInstall_EnvelopeStatusMustMatchHTTPStatus(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":200,"change":"42"}`)
	})

	client := &Client{Socket: socket}
	if _, err := client.Install(context.Background(), "smollm2"); err == nil {
		t.Fatal("expected mismatched response statuses to be rejected")
	}
}

func TestInstall_ChangeConflict(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"type":"error","status":"Conflict","result":{
			"message":"snap \"smollm2\" has \"install-snap\" change in progress",
			"kind":"snap-change-conflict"
		}}`)
	})

	client := &Client{Socket: socket}
	_, err := client.Install(context.Background(), "smollm2")
	if !errors.Is(err, ErrChangeConflict) {
		t.Fatalf("expected ErrChangeConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), `has "install-snap" change in progress`) {
		t.Fatalf("got %q, want snapd's own message to be preserved", err)
	}
}

func TestRemove_NotInstalledKeepsSnapdMessage(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","status":"Bad Request","result":{
			"message":"snap \"smollm2\" is not installed",
			"kind":"snap-not-installed"
		}}`)
	})

	client := &Client{Socket: socket}
	_, err := client.Remove(context.Background(), "smollm2")
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("expected ErrNotInstalled, got %v", err)
	}
	if !strings.Contains(err.Error(), `snap "smollm2" is not installed`) {
		t.Fatalf("got %q, want snapd's own message to be preserved", err)
	}
}

func TestRemove_NotInstalled(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","status":"Bad Request","result":{
			"message":"snap \"smollm2\" is not installed",
			"kind":"snap-not-installed"
		}}`)
	})

	client := &Client{Socket: socket}
	_, err := client.Remove(context.Background(), "smollm2")
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("expected ErrNotInstalled, got %v", err)
	}
}

func TestChange_DecodesTasksAndProgress(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/changes/42" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":{
			"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap",
			"tasks":[{
				"id":"17","kind":"download-snap","summary":"Downloading snap smollm2","status":"Doing",
				"log":["download started"],"progress":{"label":"smollm2","done":1,"total":4},
				"spawn-time":"2026-09-10T12:00:00Z"
			}]
		}}`)
	})

	client := &Client{Socket: socket}
	change, err := client.Change(context.Background(), "42")
	if err != nil {
		t.Fatalf("Change: %v", err)
	}
	if change.Ready || change.Status != "Doing" {
		t.Fatalf("got %+v", change)
	}
	if len(change.Tasks) != 1 || change.Tasks[0].ID != "17" || change.Tasks[0].Kind != "download-snap" ||
		change.Tasks[0].Progress.Label != "smollm2" || change.Tasks[0].Progress.Total != 4 ||
		len(change.Tasks[0].Log) != 1 || change.Tasks[0].SpawnTime.IsZero() {
		t.Fatalf("got tasks %+v", change.Tasks)
	}
}

func TestChange_DecodesMaintenance(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":{
			"status":"Wait","ready":false
		},"maintenance":{"kind":"system-restart","message":"system restart required"}}`)
	})

	client := &Client{Socket: socket}
	change, err := client.Change(context.Background(), "42")
	if err != nil {
		t.Fatalf("Change: %v", err)
	}
	if change.Maintenance == nil || change.Maintenance.Kind != "system-restart" {
		t.Fatalf("got maintenance %+v", change.Maintenance)
	}
}

func TestChange_RejectsMalformedEnvelope(t *testing.T) {
	tests := map[string]string{
		"unexpected type": `{"type":"async","status":"Accepted","result":{}}`,
		"missing result":  `{"type":"sync","status":"OK"}`,
		"null result":     `{"type":"sync","status":"OK","result":null}`,
	}

	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, response)
			})

			client := &Client{Socket: socket}
			if _, err := client.Change(context.Background(), "42"); err == nil {
				t.Fatal("expected malformed envelope to be rejected")
			}
		})
	}
}

func TestAbort_PostsAbortAction(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/changes/42" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("got Content-Type %q, want application/json", got)
		}
		var request struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if request.Action != "abort" {
			t.Errorf("got action %q, want abort", request.Action)
		}
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
	})

	client := &Client{Socket: socket}
	if err := client.Abort(context.Background(), "42"); err != nil {
		t.Fatalf("Abort: %v", err)
	}
}

func TestChangesInProgress_FiltersByNameAndSelector(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/changes" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.URL.Query().Get("select") != "in-progress" || r.URL.Query().Get("for") != "smollm2" {
			t.Errorf("unexpected query %q", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":[{
			"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"
		}]}`)
	})

	client := &Client{Socket: socket}
	changes, err := client.ChangesInProgress(context.Background(), "smollm2")
	if err != nil {
		t.Fatalf("ChangesInProgress: %v", err)
	}
	if len(changes) != 1 || changes[0].Summary != `Install "smollm2" snap` {
		t.Fatalf("got %+v", changes)
	}
}

func TestChangesInProgress_EmptyResult(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"sync","status":"OK","result":[]}`)
	})

	client := &Client{Socket: socket}
	changes, err := client.ChangesInProgress(context.Background(), "smollm2")
	if err != nil {
		t.Fatalf("ChangesInProgress: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected no in-progress changes, got %+v", changes)
	}
}

func TestChangesInProgress_RejectsMalformedEnvelope(t *testing.T) {
	tests := map[string]string{
		"unexpected type": `{"type":"async","status":"Accepted","result":[]}`,
		"missing result":  `{"type":"sync","status":"OK"}`,
		"null result":     `{"type":"sync","status":"OK","result":null}`,
	}

	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, response)
			})

			client := &Client{Socket: socket}
			if _, err := client.ChangesInProgress(context.Background(), "smollm2"); err == nil {
				t.Fatal("expected malformed envelope to be rejected")
			}
		})
	}
}
