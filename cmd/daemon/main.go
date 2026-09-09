package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/canonical/inference/internal/openaiproxy"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

const (
	defaultAddress  = "127.0.0.1:8000"
	requestTimeout  = 15 * time.Second
	shutdownTimeout = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	providerRoot := providers.DefaultShareProvidersPath()
	if providerRoot == "" {
		logger.Error("provider directory is not configured", "environment", providers.ShareProvidersEnvVar)
		os.Exit(1)
	}

	client := &http.Client{Timeout: requestTimeout}
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
		Addr:              defaultAddress,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
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
