package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func encodeContent(t *testing.T, data string) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func newContentsServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestSnapcraftName_Success(t *testing.T) {
	server := newContentsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/canonical/glm-4.7-flash-snap/contents/snap/snapcraft.yaml" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		body, _ := json.Marshal(contentsResponse{
			Content:  encodeContent(t, "name: glm-4-7-flash\nbase: core24\n"),
			Encoding: "base64",
		})
		w.Write(body)
	})

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL, UserAgent: "test"}
	name, err := client.SnapcraftName(context.Background(), "canonical/glm-4.7-flash-snap")
	if err != nil {
		t.Fatalf("SnapcraftName: %v", err)
	}
	if name != "glm-4-7-flash" {
		t.Fatalf("got %q, want glm-4-7-flash", name)
	}
}

func TestSnapcraftName_RejectsInvalidFullName(t *testing.T) {
	client := &GitHubClient{HTTPClient: http.DefaultClient}
	if _, err := client.SnapcraftName(context.Background(), "../etc/passwd"); err == nil {
		t.Fatal("expected error for invalid full name, got nil")
	}
}

func TestSnapcraftName_MissingFile(t *testing.T) {
	server := newContentsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	})

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	if _, err := client.SnapcraftName(context.Background(), "canonical/missing-snap"); err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestSnapcraftName_InvalidBase64(t *testing.T) {
	server := newContentsServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(contentsResponse{Content: "not-base64!!!", Encoding: "base64"})
		w.Write(body)
	})

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	if _, err := client.SnapcraftName(context.Background(), "canonical/some-snap"); err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
}

func TestSnapcraftName_MalformedJSON(t *testing.T) {
	server := newContentsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{not json`))
	})

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	if _, err := client.SnapcraftName(context.Background(), "canonical/some-snap"); err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestSnapcraftName_MalformedYAML(t *testing.T) {
	server := newContentsServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(contentsResponse{
			Content:  encodeContent(t, "name: [unterminated"),
			Encoding: "base64",
		})
		w.Write(body)
	})

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	if _, err := client.SnapcraftName(context.Background(), "canonical/some-snap"); err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestSnapcraftName_OversizedResponse(t *testing.T) {
	server := newContentsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":"` + strings.Repeat("A", maxContentBytes+10) + `","encoding":"base64"}`))
	})

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	if _, err := client.SnapcraftName(context.Background(), "canonical/some-snap"); err == nil {
		t.Fatal("expected error for oversized response, got nil")
	}
}

func TestSnapcraftName_Timeout(t *testing.T) {
	server := newContentsServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	})

	client := &GitHubClient{
		HTTPClient: &http.Client{Timeout: 10 * time.Millisecond},
		BaseURL:    server.URL,
	}
	if _, err := client.SnapcraftName(context.Background(), "canonical/some-snap"); err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestSnapcraftName_APIError(t *testing.T) {
	server := newContentsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	if _, err := client.SnapcraftName(context.Background(), "canonical/some-snap"); err == nil {
		t.Fatal("expected error for API error, got nil")
	}
}
