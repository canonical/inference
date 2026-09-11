package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestShareProvidersPath(t *testing.T) {
	t.Run("override", func(t *testing.T) {
		t.Setenv(sharedProvidersPathEnvVar, "/override/providers")
		t.Setenv("SNAP", "/snap/inference/current")
		if got := shareProvidersPath(); got != "/override/providers" {
			t.Fatalf("got %q, want override path", got)
		}
	})

	t.Run("snap default", func(t *testing.T) {
		t.Setenv(sharedProvidersPathEnvVar, "")
		t.Setenv("SNAP", "/snap/inference/current")
		want := filepath.Join("/snap/inference/current", "share/providers")
		if got := shareProvidersPath(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("not configured", func(t *testing.T) {
		t.Setenv(sharedProvidersPathEnvVar, "")
		t.Setenv("SNAP", "")
		if got := shareProvidersPath(); got != "" {
			t.Fatalf("got %q, want empty path", got)
		}
	})
}

func TestListenAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
		wantErr bool
	}{
		{name: "defaults", want: "127.0.0.1:8400"},
		{name: "configured", address: "[::1]:9000", want: "[::1]:9000"},
		{name: "missing port", address: "127.0.0.1", wantErr: true},
		{name: "invalid port", address: "127.0.0.1:invalid", wantErr: true},
		{name: "port out of range", address: "127.0.0.1:65536", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(bindAddressEnvVar, tt.address)

			got, err := listenAddress()
			if (err != nil) != tt.wantErr {
				t.Fatalf("listenAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("listenAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpstreamClientAllowsSlowInferenceHeaders(t *testing.T) {
	client := newUpstreamClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.ResponseHeaderTimeout != 0 {
		t.Errorf("response header timeout = %v, want no timeout", transport.ResponseHeaderTimeout)
	}
	if transport.MaxResponseHeaderBytes != maxResponseHeaderBytes {
		t.Errorf("maximum response header bytes = %d, want %d", transport.MaxResponseHeaderBytes, maxResponseHeaderBytes)
	}
}

func TestCatalogClientHasTimeout(t *testing.T) {
	if got := newCatalogClient().Timeout; got != catalogRequestTimeout {
		t.Fatalf("timeout=%v, want %v", got, catalogRequestTimeout)
	}
}

func TestRefreshCatalogPeriodically(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	refreshed := make(chan struct{}, 1)
	done := make(chan struct{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		refreshCatalogPeriodically(ctx, time.Hour, func(context.Context) error {
			refreshed <- struct{}{}
			return nil
		}, logger)
		close(done)
	}()

	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("catalog was not refreshed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic refresh did not stop after cancellation")
	}
}

func TestRefreshCatalogPeriodicallyDoesNotOverlap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		refreshCatalogPeriodically(ctx, time.Millisecond, func(context.Context) error {
			current := active.Add(1)
			if current > maximum.Load() {
				maximum.Store(current)
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			return errors.New("refresh failed")
		}, logger)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("catalog refresh did not start")
	}
	select {
	case <-started:
		t.Fatal("second catalog refresh overlapped the first")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second catalog refresh did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic refresh did not stop")
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent refreshes=%d, want 1", got)
	}
}

func TestShutdownServerDrainsInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	serverContext, cancelRequests := context.WithCancelCause(context.Background())
	defer cancelRequests(nil)

	testServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	}))
	testServer.Config.BaseContext = func(net.Listener) context.Context {
		return serverContext
	}
	testServer.Start()
	defer testServer.Close()

	requestDone := make(chan error, 1)
	go func() {
		response, err := testServer.Client().Get(testServer.URL)
		if err == nil {
			_, err = io.ReadAll(response.Body)
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	<-started

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownServer(testServer.Config, cancelRequests, nil, time.Second)
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before request drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	if err := <-requestDone; err != nil {
		t.Fatalf("in-flight request failed: %v", err)
	}
}

func TestShutdownServerCancelsRequestAtDeadline(t *testing.T) {
	started := make(chan struct{})
	requestCause := make(chan error, 1)
	serverContext, cancelRequests := context.WithCancelCause(context.Background())
	defer cancelRequests(nil)

	testServer := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		requestCause <- context.Cause(r.Context())
	}))
	testServer.Config.BaseContext = func(net.Listener) context.Context {
		return serverContext
	}
	testServer.Start()
	defer testServer.Close()

	requestDone := make(chan struct{})
	go func() {
		response, err := testServer.Client().Get(testServer.URL)
		if err == nil {
			_ = response.Body.Close()
		}
		close(requestDone)
	}()
	<-started

	err := shutdownServer(testServer.Config, cancelRequests, nil, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if cause := <-requestCause; !errors.Is(cause, http.ErrServerClosed) {
		t.Fatalf("request cancellation cause = %v, want server closed", cause)
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("client request did not finish after forced shutdown")
	}
}

func TestHijackedConnectionsCloseDuringShutdown(t *testing.T) {
	tracker := newHijackedConnections()
	serverConnection, clientConnection := net.Pipe()
	defer clientConnection.Close()

	tracker.connState(serverConnection, http.StateHijacked)
	tracker.close()

	if _, err := clientConnection.Write([]byte("probe")); err == nil {
		t.Fatal("hijacked connection remained open")
	}

	lateServerConnection, lateClientConnection := net.Pipe()
	defer lateClientConnection.Close()
	tracker.connState(lateServerConnection, http.StateHijacked)
	if _, err := lateClientConnection.Write([]byte("probe")); err == nil {
		t.Fatal("connection hijacked during shutdown remained open")
	}
}
