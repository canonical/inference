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
	"path/filepath"
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
	sharedProvidersPathEnvVar = "SHARED_PROVIDERS_PATH"
	httpHostEnvVar            = "HTTP_HOST"
	httpPortEnvVar            = "HTTP_PORT"
	defaultHost               = "127.0.0.1"
	defaultPort               = 8400
	catalogRefreshInterval    = 12 * time.Hour
	catalogRequestTimeout     = 30 * time.Second
	maxResponseHeaderBytes    = 1024 * 1024
	serverReadHeaderTimeout   = 10 * time.Second
	serverIdleTimeout         = 2 * time.Minute
	serverMaxHeaderBytes      = 1024 * 1024
	shutdownTimeout           = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	options, err := serverOptionsFromEnvironment()
	if err != nil {
		logger.Error("reading server configuration", "error", err)
		os.Exit(1)
	}

	address, err := listenAddress(options.host, options.port)
	if err != nil {
		logger.Error("configuring listen address", "error", err)
		os.Exit(1)
	}

	providerRoot := shareProvidersPath()
	if providerRoot == "" {
		logger.Error("provider directory is not configured", "environment", sharedProvidersPathEnvVar)
		os.Exit(1)
	}

	catalog := snapcatalog.NewReader()
	go refreshCatalogPeriodically(ctx, catalogRefreshInterval, logger)

	snapdClient := snapd.NewClient()
	listProviders := func(ctx context.Context) ([]providers.Provider, error) {
		return providers.ListAll(ctx, catalog, snapdClient, providerRoot)
	}

	handler := openaiproxy.NewModelsHandler(listProviders, newUpstreamClient(), logger)
	if err := handler.Refresh(ctx); err != nil {
		logger.Warn("initializing provider models", "error", err)
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

	logger.Info("starting server", "address", server.Addr)
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

func newCatalogClient() *http.Client {
	return &http.Client{Timeout: catalogRequestTimeout}
}

func refreshCatalog(
	ctx context.Context,
	logger *slog.Logger,
) {
	catalogRefresher := snapcatalog.NewRefresher(newCatalogClient())
	if err := catalogRefresher.Refresh(ctx); err != nil {
		logger.Warn("refreshing snap catalog", "error", err)
		return
	}
	logger.Info("refreshed snap catalog")
}

func refreshCatalogPeriodically(
	ctx context.Context,
	interval time.Duration,
	logger *slog.Logger,
) {
	refreshCatalog(ctx, logger)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCatalog(ctx, logger)
		}
	}
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

// ReverseProxy hijacks downstream connections for WebSocket upgrades, and
// http.Server.Shutdown does not close hijacked connections, so track them here
// to ensure daemon shutdown also disconnects WebSocket clients.
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

type serverOptions struct {
	host string
	port int
}

func serverOptionsFromEnvironment() (serverOptions, error) {
	host := os.Getenv(httpHostEnvVar)
	if host == "" {
		host = defaultHost
	}

	portValue := os.Getenv(httpPortEnvVar)
	if portValue == "" {
		return serverOptions{host: host, port: defaultPort}, nil
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		return serverOptions{}, fmt.Errorf("%s must be an integer: %w", httpPortEnvVar, err)
	}
	return serverOptions{host: host, port: port}, nil
}

func listenAddress(host string, port int) (string, error) {
	if host == "" {
		return "", errors.New("host must not be empty")
	}
	if port < 1 || port > 65535 {
		return "", errors.New("port must be an integer between 1 and 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func shareProvidersPath() string {
	if path := os.Getenv(sharedProvidersPathEnvVar); path != "" {
		return path
	}
	if snapRoot := os.Getenv("SNAP"); snapRoot != "" {
		return filepath.Join(snapRoot, "share/providers")
	}
	return ""
}
