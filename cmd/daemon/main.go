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
	defaultBindPort         = 8000
	responseHeaderTimeout   = 10 * time.Second
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

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	transport.MaxResponseHeaderBytes = maxResponseHeaderBytes
	client := &http.Client{Transport: transport}
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
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
		BaseContext: func(net.Listener) context.Context {
			return context.Background()
		},
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutting down daemon", "error", err)
		}
	}()

	logger.Info("starting inference daemon", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serving requests", "error", err)
		os.Exit(1)
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
