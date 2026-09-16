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
	var modelRequests atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		modelRequests.Add(1)
		if _, err := w.Write([]byte(`{"data":[{"id":"model"}]}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(healthy.Close)

	healthyModelsField := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if _, err := w.Write([]byte(`{"models":[{"name":"model"}]}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(healthyModelsField.Close)

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if _, err := w.Write([]byte(`{"data":[],"object":"list"}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(empty.Close)

	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"data":`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(malformed.Close)

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
		{Name: "healthy-models-field", State: StateEnabled, BaseURL: healthyModelsField.URL + "/v1"},
		{Name: "empty", State: StateEnabled, BaseURL: empty.URL + "/v3"},
		{Name: "malformed", State: StateEnabled, BaseURL: malformed.URL + "/v1"},
		{Name: "error", State: StateEnabled, BaseURL: failing.URL + "/v1"},
		{Name: "unresponsive", State: StateEnabled, BaseURL: unresponsive.URL + "/v1"},
		{Name: "offline", State: StateEnabled, BaseURL: offlineURL},
		{Name: "disabled", State: StateDisabled, BaseURL: healthy.URL + "/v1"},
		{Name: "no-url", State: StateEnabled},
	})

	want := map[string]HealthStatus{
		"healthy":              HealthOK,
		"healthy-models-field": HealthOK,
		"empty":                HealthError,
		"malformed":            HealthError,
		"error":                HealthError,
		"unresponsive":         HealthNotResponsive,
		"offline":              HealthOffline,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if modelRequests.Load() != 1 {
		t.Fatalf("got %d model requests, want 1", modelRequests.Load())
	}
}
