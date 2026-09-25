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
	"github.com/mattn/go-runewidth"
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

func writeAsyncAccepted(w http.ResponseWriter, changeID string) {
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"type":"async","status":"Accepted","status-code":202,"change":%q}`, changeID)
}

func TestProgressPrinter_NonTerminalPrintsEachStartedTaskOnce(t *testing.T) {
	var buf bytes.Buffer
	progress := newProgressPrinter(&buf)

	change := snapd.Change{Tasks: []snapd.Task{
		{
			ID:      "1",
			Summary: "Mount snap",
			Status:  "Done",
		},
		{
			ID:      "2",
			Summary: "Download component",
			Status:  "Doing",
			Progress: snapd.TaskProgress{
				Done:  10,
				Total: 100,
			},
		},
		{
			ID:      "3",
			Summary: "Run install hook",
			Status:  "Do",
		},
	}}
	progress.Update(change)
	change.Tasks[1].Progress.Done = 20
	progress.Update(change)
	progress.Finished()

	if got, want := buf.String(), "Mount snap\nDownload component\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProgressPrinter_NonTerminalFilePrintsSpinnerMessageOnce(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	progress := newProgressPrinter(w)
	progress.Spin("Download component")
	progress.Spin("Download component")
	progress.Finished()
	w.Close()

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if got, want := string(buf[:n]), "Download component\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProgressPrinter_SpinPlacesSpinnerNextToLabel(t *testing.T) {
	var buf bytes.Buffer
	progress := &progressPrinter{
		w:        &buf,
		terminal: true,
		now:      func() time.Time { return time.Unix(0, 0) },
		width:    func() int { return 40 },
		taskID:   "download",
		lastLog:  make(map[string]string),
		reported: make(map[string]struct{}),
	}

	progress.Spin("Download component")

	if got := buf.String(); !strings.Contains(got, "Download component /") {
		t.Fatalf("expected spinner adjacent to label, got %q", got)
	}
}

func TestActiveTaskPrefersLastTaskOverEarlierMonitor(t *testing.T) {
	tasks := []snapd.Task{
		{ID: "1", Kind: "process-delayed-security-backend-effects", Summary: "Process delayed security backend side effects", Status: "Doing"},
		{ID: "2", Kind: "download-component", Summary: "Download component", Status: "Doing"},
	}

	got := activeTask(tasks)
	if got == nil || got.ID != "2" {
		t.Fatalf("got %+v, want task 2", got)
	}
}

func TestActiveTaskSkipsTrailingMonitorWhileOtherTaskIsDoing(t *testing.T) {
	tasks := []snapd.Task{
		{ID: "1", Kind: "download-component", Summary: "Download component", Status: "Doing"},
		{ID: "2", Kind: "check-rerefresh", Summary: "Check for re-refresh", Status: "Doing"},
	}

	got := activeTask(tasks)
	if got == nil || got.ID != "1" {
		t.Fatalf("got %+v, want task 1", got)
	}
}

func TestProgressPrinter_RendersDownloadMetrics(t *testing.T) {
	var buf bytes.Buffer
	now := time.Unix(2, 0)
	progress := &progressPrinter{
		w:          &buf,
		terminal:   true,
		now:        func() time.Time { return now },
		width:      func() int { return 60 },
		taskID:     "download",
		started:    time.Unix(1, 0),
		cursorHide: true,
		lastLog:    make(map[string]string),
	}

	progress.Update(snapd.Change{Tasks: []snapd.Task{{
		ID:      "download",
		Summary: "Download component",
		Status:  "Doing",
		Progress: snapd.TaskProgress{
			Done:  25_000,
			Total: 100_000,
		},
	}}})

	got := buf.String()
	if !strings.Contains(got, " 25% 25.0kB/s 3.00s") {
		t.Fatalf("expected percentage, speed, and ETA, got %q", got)
	}
	if !strings.Contains(got, reverseVideo) {
		t.Fatalf("expected progress bar rendering, got %q", got)
	}
}

func TestProgressFormattingUsesStableSnapWidths(t *testing.T) {
	speedTests := []struct {
		bytesPerSecond float64
		want           string
	}{
		{0, "    0B/s"},
		{96_200, "96.2kB/s"},
		{932_000, " 932kB/s"},
		{1_230_000, "1.23MB/s"},
	}
	for _, test := range speedTests {
		got := formatBPS(test.bytesPerSecond, 1)
		if got != test.want || runewidth.StringWidth(got) != 8 {
			t.Fatalf("formatBPS(%v) = %q, want %q with width 8", test.bytesPerSecond, got, test.want)
		}
	}

	etaTests := []struct {
		seconds float64
		want    string
	}{
		{2.8, "2.80s"},
		{180, "3m00s"},
		{900, "15.0m"},
		{3_600, "60.0m"},
		{6_000, "1h40m"},
	}
	for _, test := range etaTests {
		got := formatETA(1, 2, test.seconds)
		if got != test.want || runewidth.StringWidth(got) != 5 {
			t.Fatalf("formatETA(%v) = %q, want %q with width 5", test.seconds, got, test.want)
		}
	}
}

func TestProgressPrinter_AdvancesSpinner(t *testing.T) {
	var buf bytes.Buffer
	progress := &progressPrinter{
		w:        &buf,
		terminal: true,
		now:      time.Now,
		width:    func() int { return 30 },
		lastLog:  make(map[string]string),
	}
	change := snapd.Change{Tasks: []snapd.Task{{
		ID:       "hook",
		Summary:  "Run install hook",
		Status:   "Doing",
		Progress: snapd.TaskProgress{Total: 1},
	}}}

	progress.Update(change)
	progress.Update(change)

	if got := buf.String(); !strings.Contains(got, " /") || !strings.Contains(got, " -") {
		t.Fatalf("spinner did not advance, got %q", got)
	}
}

func TestProgressPrinter_PrintsEachTaskLogOnce(t *testing.T) {
	var buf bytes.Buffer
	progress := newProgressPrinter(&buf)
	change := snapd.Change{Tasks: []snapd.Task{{
		ID:      "download",
		Summary: "Download component",
		Status:  "Doing",
		Log:     []string{"download started"},
	}}}

	progress.Update(change)
	progress.Update(change)

	if got, want := buf.String(), "Download component\ndownload started\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunInstall_PollsUntilDone(t *testing.T) {
	var changeRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			writeAsyncAccepted(w, "7")
		case r.URL.Path == "/v2/changes/7":
			if atomic.AddInt32(&changeRequests, 1) < 3 {
				fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap","tasks":[{"id":"1","summary":"Download snap","status":"Doing","log":["download started"],"progress":{"done":1,"total":4}}]}}`)
				return
			}
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Done","ready":true,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
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
			writeAsyncAccepted(w, "7")
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Error","ready":true,"err":"boom"}}`)
		}
	})

	client := &snapd.Client{Socket: socket}
	err := runInstall(context.Background(), client, "smollm2", nil)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("got %v, want error \"boom\"", err)
	}
}

func TestRunInstall_RetriesTransientPollFailure(t *testing.T) {
	var changeRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			writeAsyncAccepted(w, "7")
		case r.URL.Path == "/v2/changes/7":
			if atomic.AddInt32(&changeRequests, 1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"type":"error","status":"Service Unavailable","status-code":503,"result":{"message":"snapd is restarting"}}`)
				return
			}
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Done","ready":true}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
	if err := runInstall(context.Background(), client, "smollm2", nil); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if got := atomic.LoadInt32(&changeRequests); got != 2 {
		t.Fatalf("got %d change requests, want 2", got)
	}
}

func TestRunInstall_SurfacesSystemRestartMaintenance(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			writeAsyncAccepted(w, "7")
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{
				"status":"Wait","ready":false
			},"maintenance":{"kind":"system-restart","message":"system restart required"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
	err := runInstall(context.Background(), client, "smollm2", nil)
	if err == nil || !strings.Contains(err.Error(), "system restart required for change 7") {
		t.Fatalf("got %v, want a system restart error containing the change id", err)
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
			writeAsyncAccepted(w, "7")
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

	client := &snapd.Client{Socket: socket}
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

	client := &snapd.Client{Socket: socket}
	err := runInstall(context.Background(), client, "smollm2", nil)
	if err == nil {
		t.Fatal("expected an error when waiting out the conflict fails")
	}
	if errors.Is(err, snapd.ErrChangeConflict) {
		t.Fatalf("expected the underlying wait error, not the original conflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "waiting for conflicting change on smollm2") {
		t.Fatalf("expected the error to identify the conflict wait, got %v", err)
	}
}

func TestRunInstall_RepeatedConflictsStopAfterBoundedRetries(t *testing.T) {
	var installRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			atomic.AddInt32(&installRequests, 1)
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"type":"error","status":"Conflict","result":{
				"message":"snap \"smollm2\" has \"install-snap\" change in progress",
				"kind":"snap-change-conflict"
			}}`)
		case r.URL.Path == "/v2/changes":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":[]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
	err := runInstall(context.Background(), client, "smollm2", nil)
	if !errors.Is(err, snapd.ErrChangeConflict) {
		t.Fatalf("expected the final conflict to surface, got %v", err)
	}
	if got := atomic.LoadInt32(&installRequests); got != maxConflictRetries+1 {
		t.Fatalf("expected %d attempts, got %d", maxConflictRetries+1, got)
	}
}

func TestRunInstall_SynchronousResponseIsRejected(t *testing.T) {
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
	if err := runInstall(context.Background(), client, "smollm2", nil); err == nil {
		t.Fatal("expected a synchronous action response to be rejected")
	}
}

func TestRunInstall_ContextCancellationDuringPoll(t *testing.T) {
	var abortRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/smollm2":
			writeAsyncAccepted(w, "7")
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			atomic.AddInt32(&abortRequests, 1)
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
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
			writeAsyncAccepted(w, "7")
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	err := InstallSnap(ctx, &Context{Stdout: &bytes.Buffer{}, SnapdClient: client}, "smollm2")
	if err == nil || err.Error() != "installation cancelled" {
		t.Fatalf("got %v, want \"installation cancelled\"", err)
	}
}

func TestRunRemove_ContextCancellationDuringPoll(t *testing.T) {
	var abortRequests int32
	socket := newUnixServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/smollm2":
			writeAsyncAccepted(w, "7")
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			atomic.AddInt32(&abortRequests, 1)
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Remove \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
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
			writeAsyncAccepted(w, "7")
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"type":"error","status":"Internal Server Error","result":{"message":"boom"}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Install \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	err := InstallSnap(ctx, &Context{Stdout: &bytes.Buffer{}, SnapdClient: client}, "smollm2")
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
			writeAsyncAccepted(w, "7")
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{}}`)
		case r.URL.Path == "/v2/changes/7":
			fmt.Fprint(w, `{"type":"sync","status":"OK","result":{"status":"Doing","ready":false,"summary":"Remove \"smollm2\" snap"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	client := &snapd.Client{Socket: socket}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	err := RemoveSnap(ctx, &Context{Stdout: &bytes.Buffer{}, SnapdClient: client}, "smollm2")
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
			writeAsyncAccepted(w, "7")
		case r.Method == http.MethodPost && r.URL.Path == "/v2/changes/7":
			t.Error("abort must not be issued for a change that is already ready")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"type":"error","status":"Bad Request","result":{"message":"cannot abort change 7 with nothing pending"}}`)
		case r.URL.Path == "/v2/changes/7":
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

	client := &snapd.Client{Socket: socket}
	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn
	defer cancelFn()

	if err := runInstall(ctx, client, "smollm2", nil); err != nil {
		t.Fatalf("got %v, want the completed install to be reported as success", err)
	}
}
