package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/canonical/inference/internal/openaiproxy"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

const (
	bindHostEnvVar          = "INFERENCE_BIND_HOST"
	bindPortEnvVar          = "INFERENCE_BIND_PORT"
	defaultBindHost         = "127.0.0.1"
	defaultBindPort         = 8400
	maxResponseHeaderBytes  = 1 << 20
	serverReadHeaderTimeout = 10 * time.Second
	serverIdleTimeout       = 2 * time.Minute
	serverMaxHeaderBytes    = 1 << 20
	shutdownTimeout         = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	address, err := listenAddress()
	if err != nil {
		logger.Error("configuring listen address", "error", err)
		os.Exit(1)
	}
	providerRoot := providers.DefaultShareProvidersPath()
	if providerRoot == "" {
		logger.Error("provider directory is not configured", "environment", providers.ShareProvidersEnvVar)
		os.Exit(1)
	}
	client := newUpstreamClient()
	catalog := snapcatalog.NewReader()
	snapdClient := snapd.NewClient()
	listProviders := func(ctx context.Context) ([]providers.Provider, error) {
		return providers.List(ctx, catalog, snapdClient, providerRoot, providers.ListOptions{})
	}
	handler := openaiproxy.NewModelsHandler(listProviders, client, logger)
	if err := handler.Refresh(ctx); err != nil {
		logger.Error("initializing provider models", "error", err)
		os.Exit(1)
	}
	serverContext, cancelRequests := context.WithCancelCause(context.Background())
	defer cancelRequests(nil)
	hijacked := newHijackedConnections()
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
		BaseContext: func(net.Listener) context.Context {
			return serverContext
		},
		ConnState: hijacked.connState,
	}

	logger.Info("starting inference daemon", "address", server.Addr)
	if err := serve(ctx, server, cancelRequests, hijacked.close, logger); err != nil {
		logger.Error("serving requests", "error", err)
		os.Exit(1)
	}
}

func newUpstreamClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 0
	transport.MaxResponseHeaderBytes = maxResponseHeaderBytes
	return &http.Client{Transport: transport}
}

func serve(
	ctx context.Context,
	server *http.Server,
	cancelRequests context.CancelCauseFunc,
	closeLongLived func(),
	logger *slog.Logger,
) error {
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	if err := shutdownServer(server, cancelRequests, closeLongLived, shutdownTimeout); err != nil {
		logger.Warn("graceful shutdown deadline reached", "error", err)
	}
	if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func shutdownServer(
	server *http.Server,
	cancelRequests context.CancelCauseFunc,
	closeLongLived func(),
	timeout time.Duration,
) error {
	if closeLongLived != nil {
		closeLongLived()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		cancelRequests(http.ErrServerClosed)
		if closeErr := server.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			return errors.Join(err, closeErr)
		}
		return err
	}
	return nil
}

type hijackedConnections struct {
	mu           sync.Mutex
	connections  map[net.Conn]struct{}
	shuttingDown bool
}

func newHijackedConnections() *hijackedConnections {
	return &hijackedConnections{connections: make(map[net.Conn]struct{})}
}

func (h *hijackedConnections) connState(connection net.Conn, state http.ConnState) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch state {
	case http.StateHijacked:
		if h.shuttingDown {
			_ = connection.Close()
			return
		}
		h.connections[connection] = struct{}{}
	case http.StateClosed:
		delete(h.connections, connection)
	}
}

func (h *hijackedConnections) close() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.shuttingDown = true
	for connection := range h.connections {
		_ = connection.Close()
		delete(h.connections, connection)
	}
}

func listenAddress() (string, error) {
	host := os.Getenv(bindHostEnvVar)
	if host == "" {
		host = defaultBindHost
	}
	port := defaultBindPort
	if value := os.Getenv(bindPortEnvVar); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return "", fmt.Errorf("%s must be an integer between 1 and 65535", bindPortEnvVar)
		}
		port = parsed
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}
