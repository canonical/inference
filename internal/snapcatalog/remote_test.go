package snapcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPFetcher_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"repositories":{}}`))
	}))
	t.Cleanup(server.Close)

	fetcher := &HTTPFetcher{URL: server.URL, HTTPClient: server.Client()}
	data, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != `{"repositories":{}}` {
		t.Fatalf("got %q", data)
	}
}

func TestHTTPFetcher_NonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	fetcher := &HTTPFetcher{URL: server.URL, HTTPClient: server.Client()}
	if _, err := fetcher.Fetch(context.Background()); err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestHTTPFetcher_Oversized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", maxResponseBytes+10)))
	}))
	t.Cleanup(server.Close)

	fetcher := &HTTPFetcher{URL: server.URL, HTTPClient: server.Client()}
	if _, err := fetcher.Fetch(context.Background()); err == nil {
		t.Fatal("expected error for oversized response, got nil")
	}
}

func TestHTTPFetcher_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(server.Close)

	fetcher := &HTTPFetcher{URL: server.URL, HTTPClient: &http.Client{Timeout: 10 * time.Millisecond}}
	if _, err := fetcher.Fetch(context.Background()); err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
