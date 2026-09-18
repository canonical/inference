package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/inference/internal/snapcatalog"
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
		host    string
		port    int
		want    string
		wantErr bool
	}{
		{name: "defaults", host: defaultHost, port: defaultPort, want: "127.0.0.1:8400"},
		{name: "configured", host: "::1", port: 9000, want: "[::1]:9000"},
		{name: "empty host", port: 9000, wantErr: true},
		{name: "port too low", host: "127.0.0.1", port: 0, wantErr: true},
		{name: "port too high", host: "127.0.0.1", port: 65536, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := listenAddress(tt.host, tt.port)
			if (err != nil) != tt.wantErr {
				t.Fatalf("listenAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("listenAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServerOptionsFromEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		port    string
		want    serverOptions
		wantErr bool
	}{
		{name: "defaults", want: serverOptions{host: defaultHost, port: defaultPort}},
		{
			name: "configured",
			host: "0.0.0.0",
			port: "9000",
			want: serverOptions{host: "0.0.0.0", port: 9000},
		},
		{name: "invalid port", port: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(httpHostEnvVar, tt.host)
			t.Setenv(httpPortEnvVar, tt.port)
			got, err := serverOptionsFromEnvironment()
			if (err != nil) != tt.wantErr {
				t.Fatalf("serverOptionsFromEnvironment() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("serverOptionsFromEnvironment() = %#v, want %#v", got, tt.want)
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

func TestRefreshCatalogLogsConfigurationError(t *testing.T) {
	t.Setenv(snapcatalog.EnvVar, "")
	t.Setenv("SNAP_COMMON", "")
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	refreshCatalog(context.Background(), logger)

	if got := output.String(); !strings.Contains(got, snapcatalog.ErrNotConfigured.Error()) {
		t.Fatalf("log output = %q, want catalog configuration error", got)
	}
}

func TestRefreshCatalogPeriodicallyRefreshesBeforeStopping(t *testing.T) {
	t.Setenv(snapcatalog.EnvVar, "")
	t.Setenv("SNAP_COMMON", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var output bytes.Buffer
	done := make(chan struct{})
	logger := slog.New(slog.NewTextHandler(&output, nil))
	go func() {
		refreshCatalogPeriodically(ctx, time.Hour, logger)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic refresh did not stop after cancellation")
	}
	if got := output.String(); !strings.Contains(got, "refreshing snap catalog") {
		t.Fatalf("log output = %q, want initial catalog refresh", got)
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
