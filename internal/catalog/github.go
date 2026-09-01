package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	githubAPIHost    = "api.github.com"
	defaultUserAgent = "inference-cli (+https://github.com/canonical/inference)"
	githubTimeout    = 10 * time.Second
	maxContentBytes  = 1 << 20 // 1 MiB bound on a fetched snapcraft.yaml
)

// fullNamePattern restricts full_name to the owner/repo shape GitHub uses,
// so it can only be used to build a request under the fixed GitHub API
// host, never an arbitrary URL.
var fullNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// GitHubClient fetches files from public GitHub repositories through the
// fixed api.github.com contents API. It never follows arbitrary catalog
// URLs: requests are always constructed from a validated full_name under
// this host.
type GitHubClient struct {
	HTTPClient *http.Client
	UserAgent  string

	// BaseURL overrides the API host, defaulting to https://api.github.com.
	// It exists only so tests can point at a local server; production code
	// never sets it, and requests are still built exclusively from a
	// validated full_name.
	BaseURL string
}

// NewGitHubClient returns a GitHubClient with a bounded timeout and a
// descriptive User-Agent.
func NewGitHubClient() *GitHubClient {
	return &GitHubClient{
		HTTPClient: &http.Client{Timeout: githubTimeout},
		UserAgent:  defaultUserAgent,
	}
}

// contentsResponse is the relevant subset of GitHub's repository contents
// API response.
type contentsResponse struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

// SnapcraftName fetches snap/snapcraft.yaml from the given repository's
// default branch and returns its authoritative, validated snap name.
func (c *GitHubClient) SnapcraftName(ctx context.Context, fullName string) (string, error) {
	if !fullNamePattern.MatchString(fullName) {
		return "", fmt.Errorf("invalid repository full name %q", fullName)
	}

	base := c.BaseURL
	if base == "" {
		base = "https://" + githubAPIHost
	}
	url := fmt.Sprintf("%s/repos/%s/contents/snap/snapcraft.yaml", base, fullName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building GitHub request for %s: %w", fullName, err)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: githubTimeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching snap/snapcraft.yaml for %s: %w", fullName, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxContentBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading GitHub response for %s: %w", fullName, err)
	}
	if len(body) > maxContentBytes {
		return "", fmt.Errorf("GitHub response for %s exceeded %d bytes", fullName, maxContentBytes)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned HTTP %d for %s", resp.StatusCode, fullName)
	}

	var content contentsResponse
	if err := json.Unmarshal(body, &content); err != nil {
		return "", fmt.Errorf("decoding GitHub response for %s: %w", fullName, err)
	}
	if content.Encoding != "base64" {
		return "", fmt.Errorf("unexpected content encoding %q for %s", content.Encoding, fullName)
	}

	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("decoding base64 content for %s: %w", fullName, err)
	}

	name, err := ParseSnapcraftName(raw)
	if err != nil {
		return "", fmt.Errorf("resolving snap name for %s: %w", fullName, err)
	}
	return name, nil
}
