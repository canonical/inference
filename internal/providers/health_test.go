package providers

import (
	"context"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckHealth(t *testing.T) {
	var fallbackRequests atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			http.Error(w, "not found", http.StatusNotFound)
		case "/v1/models":
			fallbackRequests.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(healthy.Close)

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)

	unresponsive := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(unresponsive.Close)

	offlineListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	offlineURL := "http://" + offlineListener.Addr().String() + "/v1"
	if err := offlineListener.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got := CheckHealth(ctx, []Provider{
		{Name: "healthy", State: StateEnabled, BaseURL: healthy.URL + "/v1"},
		{Name: "error", State: StateEnabled, BaseURL: failing.URL + "/v1"},
		{Name: "unresponsive", State: StateEnabled, BaseURL: unresponsive.URL + "/v1"},
		{Name: "offline", State: StateEnabled, BaseURL: offlineURL},
		{Name: "disabled", State: StateDisabled, BaseURL: healthy.URL + "/v1"},
		{Name: "no-url", State: StateEnabled},
	})

	want := map[string]HealthStatus{
		"healthy":      HealthOK,
		"error":        HealthError,
		"unresponsive": HealthNotResponsive,
		"offline":      HealthOffline,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if fallbackRequests.Load() != 1 {
		t.Fatalf("got %d fallback requests, want 1", fallbackRequests.Load())
	}
}
