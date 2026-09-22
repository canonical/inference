package snapd

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func NewFakeServer(t testing.TB, statuses map[string]string) (*Client, *atomic.Int32) {
	t.Helper()

	dir := t.TempDir()
	socket := filepath.Join(dir, "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on unix socket: %v", err)
	}

	var requestsCounter atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsCounter.Add(1)
		name := r.URL.Path[len("/v2/snaps/"):]
		status, ok := statuses[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"type":"error","status":"Not Found","status-code":404,"result":{
				"message":"snap \"%s\" not found",
				"kind":"snap-not-found"
			}}`, name)
			return
		}
		fmt.Fprintf(w, `{"type":"sync","status":"OK","result":{"name":%q,"status":%q}}`, name, status)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	return &Client{Socket: socket}, &requestsCounter
}
