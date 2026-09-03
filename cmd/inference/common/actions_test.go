package common

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/inference/internal/snapd"
)

func newUnixServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "inference-cmd-test")
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

func TestNewProgressPrinter_NonFileWriterPrintsEachLine(t *testing.T) {
	var buf bytes.Buffer
	progress, finish := NewProgressPrinter(&buf)

	progress("Download component (10%)")
	progress("Download component (20%)")
	finish()

	got := buf.String()
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("expected each update on its own line, got %q", got)
	}
	if strings.Contains(got, "\x1b[K") {
		t.Fatalf("non-terminal writer must not receive cursor control codes, got %q", got)
	}
}

func TestNewProgressPrinter_NonTerminalFilePrintsEachLine(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	progress, finish := NewProgressPrinter(w)
	progress("Download component (10%)")
	progress("Download component (20%)")
	finish()
	w.Close()

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("expected each update on its own line for a non-tty file, got %q", got)
	}
}

func TestChangeProgressMessage_PrefersLastDoingTaskOverEarlierStuckOne(t *testing.T) {
	change := snapd.Change{
		Summary: `Install "deepseek-r1" snap`,
		Tasks: []snapd.Task{
			{Summary: "Process delayed security backend side effects for affected snaps", Status: "Doing"},
			{Summary: "Download component \"model-distill-qwen-7b-ov-int4\" (162)", Status: "Doing", Progress: snapd.TaskProgress{Done: 1, Total: 100}},
		},
	}

	got := changeProgressMessage(change)
	want := `Download component "model-distill-qwen-7b-ov-int4" (162) (1.00%)`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChangeProgressMessage_FallsBackToSummaryWhenNoTaskIsDoing(t *testing.T) {
	change := snapd.Change{
		Summary: `Install "smollm2" snap`,
		Tasks: []snapd.Task{
			{Summary: "Mount snap", Status: "Done"},
		},
	}

	if got, want := changeProgressMessage(change), `Install "smollm2" snap`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunInstall_PollsUntilDone(t *testing.T) {
	var changeRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.URL.Path == "/v2/changes/7":
			if atomic.AddInt32(&changeRequests, 1) < 3 {
				fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}}`)
				return
			}
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Done","ready":true,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	var buf bytes.Buffer
	err := runInstall(context.Background(), client, "smollm2", &buf)
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected at least one progress message")
	}
}

func TestRunInstall_ReturnsErrorWhenChangeFails(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Error","ready":true,"err":"boom"}}`)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	err := runInstall(context.Background(), client, "smollm2", nil)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("got %v, want error \"boom\"", err)
	}
}

func TestRunInstall_WaitsOutConflictThenRetries(t *testing.T) {
	var installRequests int32
	var inProgressRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			if atomic.AddInt32(&installRequests, 1) == 1 {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"type":"error","status":"Conflict","result":{
					"message":"snap \"smollm2\" has \"install-snap\" change in progress",
					"kind":"snap-change-conflict"
				}}`)
				return
			}
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.URL.Path == "/v2/changes":
			if atomic.AddInt32(&inProgressRequests, 1) < 2 {
				fmt.Fprint(w, `{"type":"sync","status":"OK","result":[{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}]}`)
				return
			}
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":[]}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Done","ready":true,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	err := runInstall(context.Background(), client, "smollm2", nil)
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if atomic.LoadInt32(&installRequests) != 2 {
		t.Fatalf("expected exactly one retry after the conflict, got %d install requests", installRequests)
	}
}

func TestRunInstall_PropagatesConflictWaitFailure(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"type":"error","status":"Conflict","result":{
				"message":"snap \"smollm2\" has \"install-snap\" change in progress",
				"kind":"snap-change-conflict"
			}}`)
		case r.URL.Path == "/v2/changes":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"type":"error","status":"Internal Server Error","result":{"message":"boom"}}`)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	err := runInstall(context.Background(), client, "smollm2", nil)
	if err == nil {
		t.Fatal("expected an error when waiting out the conflict fails")
	}
	if errors.Is(err, snapd.ErrChangeConflict) {
		t.Fatalf("expected the underlying wait error, not the original conflict, got %v", err)
	}
}

func TestRunInstall_SynchronousResponseSkipsPolling(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	if err := runInstall(context.Background(), client, "smollm2", nil); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
}

func TestRunInstall_ContextCancellationDuringPoll(t *testing.T) {
	var abortRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/smollm2":
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			atomic.AddInt32(&abortRequests, 1)
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := runInstall(ctx, client, "smollm2", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
	if got := atomic.LoadInt32(&abortRequests); got != 1 {
		t.Fatalf("got %d abort requests, want 1", got)
	}
}

func TestInstallSnap_ContextCancellationHasFriendlyMessage(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/smollm2":
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	err := InstallSnap(ctx, &Context{Stdout: &bytes.Buffer{}}, client, "smollm2")
	if err == nil || err.Error() != "installation cancelled" {
		t.Fatalf("got %v, want \"installation cancelled\"", err)
	}
}

func TestRunRemove_ContextCancellationDuringPoll(t *testing.T) {
	var abortRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/smollm2":
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			atomic.AddInt32(&abortRequests, 1)
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Remove \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := runRemove(ctx, client, "smollm2", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
	if got := atomic.LoadInt32(&abortRequests); got != 1 {
		t.Fatalf("got %d abort requests, want 1", got)
	}
}

func TestInstallSnap_CancellationReportsFailedAbort(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/smollm2":
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"type":"error","status":"Internal Server Error","result":{"message":"boom"}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	err := InstallSnap(ctx, &Context{Stdout: &bytes.Buffer{}}, client, "smollm2")
	if err == nil {
		t.Fatal("expected an error when the abort fails")
	}
	if !strings.HasPrefix(err.Error(), "installation cancelled: ") {
		t.Fatalf("got %q, want the friendly cancellation prefix", err)
	}
	if !strings.Contains(err.Error(), "could not abort snap change 7") {
		t.Fatalf("got %q, want the abort failure to be reported", err)
	}
}

func TestRemoveSnap_ContextCancellationHasFriendlyMessage(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/smollm2":
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Remove \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	err := RemoveSnap(ctx, &Context{Stdout: &bytes.Buffer{}}, client, "smollm2")
	if err == nil || err.Error() != "removal cancelled" {
		t.Fatalf("got %v, want \"removal cancelled\"", err)
	}
}

func TestRunInstall_ChangeReadyAtCancellationSkipsAbort(t *testing.T) {
	var cancel context.CancelFunc
	var polls int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/smollm2":
			fmt.Fprint(w, `{"type":"async","status":"Accepted","status-code":202,"change":"7"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			t.Error("abort must not be issued for a change that is already ready")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"type":"error","status":"Bad Request","result":{"message":"cannot abort change 7 with nothing pending"}}`)
		case r.URL.Path == "/v2/changes/7":
			// The change completes in the same tick the context is cancelled.
			if atomic.AddInt32(&polls, 1) == 1 {
				cancel()
				fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}}`)
				return
			}
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Done","ready":true,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Sockets: []string{socket}}
	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn
	defer cancelFn()

	if err := runInstall(ctx, client, "smollm2", nil); err != nil {
		t.Fatalf("got %v, want the completed install to be reported as success", err)
	}
}
