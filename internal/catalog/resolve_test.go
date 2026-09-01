package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type fakeFetcher struct {
	data []byte
	err  error
}

func (f fakeFetcher) FetchCatalog(context.Context) ([]byte, error) { return f.data, f.err }

// fakeResolver maps full_name -> name, or returns errsByRepo[full_name] if set.
type fakeResolver struct {
	names      map[string]string
	errsByRepo map[string]error
}

func (f fakeResolver) SnapcraftName(_ context.Context, fullName string) (string, error) {
	if err, ok := f.errsByRepo[fullName]; ok {
		return "", err
	}
	name, ok := f.names[fullName]
	if !ok {
		return "", fmt.Errorf("no name registered for %s", fullName)
	}
	return name, nil
}

const sampleCatalog = `{
  "repositories": {
    "gemma4": {"full_name": "canonical/gemma4-snap", "html_url": "https://github.com/canonical/gemma4-snap", "model_name": "Gemma 4", "visibility": "public"},
    "glm": {"full_name": "canonical/glm-4.7-flash-snap", "html_url": "https://github.com/canonical/glm-4.7-flash-snap", "model_name": "GLM 4.7 Flash", "visibility": "public"},
    "private-one": {"full_name": "canonical/private-snap", "html_url": "https://github.com/canonical/private-snap", "model_name": "Private", "visibility": "private"}
  }
}`

func TestResolveCandidate_Success(t *testing.T) {
	fetcher := fakeFetcher{data: []byte(sampleCatalog)}
	resolver := fakeResolver{names: map[string]string{
		"canonical/gemma4-snap":        "gemma4",
		"canonical/glm-4.7-flash-snap": "glm-4-7-flash",
	}}

	providers, err := ResolveCandidate(context.Background(), fetcher, resolver, 2)
	if err != nil {
		t.Fatalf("ResolveCandidate: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("got %+v, want 2 providers (private excluded)", providers)
	}
	// Sorted by name.
	if providers[0].Name != "gemma4" || providers[1].Name != "glm-4-7-flash" {
		t.Fatalf("got %+v", providers)
	}
}

func TestResolveCandidate_FetchError(t *testing.T) {
	fetcher := fakeFetcher{err: errors.New("network down")}
	_, err := ResolveCandidate(context.Background(), fetcher, fakeResolver{}, 2)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveCandidate_PartialFailureDiscardsWholeCandidate(t *testing.T) {
	fetcher := fakeFetcher{data: []byte(sampleCatalog)}
	resolver := fakeResolver{
		names: map[string]string{"canonical/gemma4-snap": "gemma4"},
		errsByRepo: map[string]error{
			"canonical/glm-4.7-flash-snap": errors.New("missing snapcraft.yaml"),
		},
	}

	_, err := ResolveCandidate(context.Background(), fetcher, resolver, 2)
	if err == nil {
		t.Fatal("expected error for partial resolution failure, got nil")
	}
}

func TestResolveCandidate_DuplicateNamesRejected(t *testing.T) {
	fetcher := fakeFetcher{data: []byte(sampleCatalog)}
	resolver := fakeResolver{names: map[string]string{
		"canonical/gemma4-snap":        "same-name",
		"canonical/glm-4.7-flash-snap": "same-name",
	}}

	_, err := ResolveCandidate(context.Background(), fetcher, resolver, 2)
	if err == nil {
		t.Fatal("expected error for duplicate names, got nil")
	}
}

func TestResolveCandidate_EmptyCatalogRejected(t *testing.T) {
	fetcher := fakeFetcher{data: []byte(`{"repositories":{}}`)}
	_, err := ResolveCandidate(context.Background(), fetcher, fakeResolver{}, 2)
	if err == nil {
		t.Fatal("expected error for empty catalog, got nil")
	}
}

func TestResolveCandidate_AllPrivateRejected(t *testing.T) {
	fetcher := fakeFetcher{data: []byte(`{"repositories":{"a":{"full_name":"canonical/a-snap","visibility":"private"}}}`)}
	_, err := ResolveCandidate(context.Background(), fetcher, fakeResolver{}, 2)
	if err == nil {
		t.Fatal("expected error for all-private catalog, got nil")
	}
}

func TestResolveCandidate_MalformedCatalogRejected(t *testing.T) {
	fetcher := fakeFetcher{data: []byte(`not json`)}
	_, err := ResolveCandidate(context.Background(), fetcher, fakeResolver{}, 2)
	if err == nil {
		t.Fatal("expected error for malformed catalog, got nil")
	}
}
