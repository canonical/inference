package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PublicCatalogURL is the primary, publicly accessible generated catalog
// JSON.
const PublicCatalogURL = "https://raw.githubusercontent.com/canonical/inference-snaps-admin/refs/heads/main/github-projects/onboarded-snaps.generated.json"

const (
	catalogFetchTimeout = 10 * time.Second
	maxCatalogBytes     = 4 << 20 // 4 MiB bound on the published catalog JSON
)

// CatalogFetcher fetches the raw public catalog JSON bytes. Implemented by
// HTTPCatalogFetcher against the published URL, and by fakes in tests.
type CatalogFetcher interface {
	FetchCatalog(ctx context.Context) ([]byte, error)
}

// HTTPCatalogFetcher fetches the published catalog JSON over HTTP with a
// bounded timeout and response size.
type HTTPCatalogFetcher struct {
	URL        string
	HTTPClient *http.Client
	UserAgent  string
}

// NewHTTPCatalogFetcher returns a fetcher for the primary public catalog URL.
func NewHTTPCatalogFetcher() *HTTPCatalogFetcher {
	return &HTTPCatalogFetcher{
		URL:        PublicCatalogURL,
		HTTPClient: &http.Client{Timeout: catalogFetchTimeout},
		UserAgent:  defaultUserAgent,
	}
}

// FetchCatalog implements CatalogFetcher.
func (f *HTTPCatalogFetcher) FetchCatalog(ctx context.Context) ([]byte, error) {
	url := f.URL
	if url == "" {
		url = PublicCatalogURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building catalog request: %w", err)
	}
	req.Header.Set("User-Agent", f.UserAgent)

	httpClient := f.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: catalogFetchTimeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching public catalog: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading public catalog response: %w", err)
	}
	if len(body) > maxCatalogBytes {
		return nil, fmt.Errorf("public catalog response exceeded %d bytes", maxCatalogBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public catalog returned HTTP %d", resp.StatusCode)
	}

	return body, nil
}
