package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPCatalogFetcher_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"repositories":{}}`))
	}))
	t.Cleanup(server.Close)

	fetcher := &HTTPCatalogFetcher{URL: server.URL, HTTPClient: server.Client()}
	data, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if string(data) != `{"repositories":{}}` {
		t.Fatalf("got %q", data)
	}
}

func TestHTTPCatalogFetcher_NonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	fetcher := &HTTPCatalogFetcher{URL: server.URL, HTTPClient: server.Client()}
	if _, err := fetcher.FetchCatalog(context.Background()); err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestHTTPCatalogFetcher_Oversized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", maxCatalogBytes+10)))
	}))
	t.Cleanup(server.Close)

	fetcher := &HTTPCatalogFetcher{URL: server.URL, HTTPClient: server.Client()}
	if _, err := fetcher.FetchCatalog(context.Background()); err == nil {
		t.Fatal("expected error for oversized response, got nil")
	}
}

func TestHTTPCatalogFetcher_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(server.Close)

	fetcher := &HTTPCatalogFetcher{URL: server.URL, HTTPClient: &http.Client{Timeout: 10 * time.Millisecond}}
	if _, err := fetcher.FetchCatalog(context.Background()); err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
