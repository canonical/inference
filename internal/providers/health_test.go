package providers

import (
	"context"
	"errors"
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
		Provider{Name: "healthy", State: StateEnabled, BaseURL: healthy.URL + "/v1"},
		Provider{Name: "healthy-models-field", State: StateEnabled, BaseURL: healthyModelsField.URL + "/v1"},
		Provider{Name: "empty", State: StateEnabled, BaseURL: empty.URL + "/v3"},
		Provider{Name: "malformed", State: StateEnabled, BaseURL: malformed.URL + "/v1"},
		Provider{Name: "error", State: StateEnabled, BaseURL: failing.URL + "/v1"},
		Provider{Name: "unresponsive", State: StateEnabled, BaseURL: unresponsive.URL + "/v1"},
		Provider{Name: "offline", State: StateEnabled, BaseURL: offlineURL},
		Provider{Name: "disabled", State: StateDisabled, BaseURL: healthy.URL + "/v1"},
		Provider{Name: "not-connected", State: StateEnabled, Connection: ConnectionNotConnected},
		Provider{Name: "broken-config", State: StateEnabled, Connection: ConnectionConnected, BaseURL: healthy.URL + "/v1", Err: errors.New("OPENAI_BASE_URL is empty")},
	})

	want := map[string]HealthStatus{
		"healthy":              HealthOK,
		"healthy-models-field": HealthOK,
		"empty":                HealthError,
		"malformed":            HealthError,
		"error":                HealthError,
		"unresponsive":         HealthNotResponsive,
		"offline":              HealthOffline,
		"broken-config":        HealthError,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if modelRequests.Load() != 1 {
		t.Fatalf("got %d model requests, want 1", modelRequests.Load())
	}
}

func TestCheckHealthWithoutQueryableProviders(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		want     map[string]HealthStatus
	}{
		{
			name: "provider error",
			provider: Provider{
				Name: "provider", Backend: BackendSnap, Connection: ConnectionConnected,
				Err: errors.New("invalid configuration"),
			},
			want: map[string]HealthStatus{"provider": HealthError},
		},
		{
			name:     "not connected",
			provider: Provider{Name: "provider", Backend: BackendSnap, State: StateEnabled, Connection: ConnectionNotConnected},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := CheckHealth(context.Background(), []Provider{test.provider})
			if !maps.Equal(got, test.want) || (got == nil) != (test.want == nil) {
				t.Fatalf("got %#v, want %#v", got, test.want)
			}
		})
	}
}
