package snapcatalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const PublicURL = "https://canonical.github.io/inference-snaps-admin/onboarded-snaps.html"

const (
	fetchTimeout     = 10 * time.Second
	maxResponseBytes = 4 << 20
	defaultUserAgent = "inference-cli (+https://github.com/canonical/inference)"
)

type Fetcher interface {
	Fetch(ctx context.Context) ([]byte, error)
}

type HTTPFetcher struct {
	URL        string
	HTTPClient *http.Client
	UserAgent  string
}

func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{
		URL:        PublicURL,
		HTTPClient: &http.Client{Timeout: fetchTimeout},
		UserAgent:  defaultUserAgent,
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context) ([]byte, error) {
	url := f.URL
	if url == "" {
		url = PublicURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building catalog request: %w", err)
	}
	req.Header.Set("User-Agent", f.UserAgent)

	httpClient := f.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: fetchTimeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching public snap catalog: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading public snap catalog response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("public snap catalog response exceeded %d bytes", maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public snap catalog returned HTTP %d", resp.StatusCode)
	}

	return body, nil
}
