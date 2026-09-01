package providers

import (
	"context"
	"errors"
	"testing"
)

type fakeCatalog struct {
	names    []string
	warnings []string
	err      error
}

func (f fakeCatalog) Resolve(context.Context) ([]string, []string, error) {
	return f.names, f.warnings, f.err
}

type fakeSnaps struct {
	statuses map[string]string
	err      error
}

func (f fakeSnaps) Statuses(context.Context) (map[string]string, error) {
	return f.statuses, f.err
}

func TestList_InstalledProviderGetsSnapdStatus(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"gemma4"}},
		fakeSnaps{statuses: map[string]string{"gemma4": "active"}},
	)

	got, warnings, err := svc.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	want := []Provider{{Name: "gemma4", Type: InferenceSnap, Status: "active"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestList_AbsentProviderIsNotInstalled(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"gemma4"}},
		fakeSnaps{statuses: map[string]string{}},
	)

	got, _, err := svc.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Status != StatusNotInstalled {
		t.Fatalf("got %+v, want status %q", got, StatusNotInstalled)
	}
}

func TestList_UnknownSnapdStatusIsPreserved(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"gemma4"}},
		fakeSnaps{statuses: map[string]string{"gemma4": "some-future-status"}},
	)

	got, _, err := svc.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got[0].Status != "some-future-status" {
		t.Fatalf("got status %q, want preserved value", got[0].Status)
	}
}

func TestList_EmptyStatusForMatchingProviderFails(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"gemma4"}},
		fakeSnaps{statuses: map[string]string{"gemma4": ""}},
	)

	_, _, err := svc.List(context.Background(), ListOptions{})
	if err == nil {
		t.Fatal("expected error for empty snapd status, got nil")
	}
}

func TestList_InstalledOnlyExcludesNotInstalled(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"gemma4", "qwen3"}},
		fakeSnaps{statuses: map[string]string{"gemma4": "active"}},
	)

	got, _, err := svc.List(context.Background(), ListOptions{InstalledOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "gemma4" {
		t.Fatalf("got %+v, want only gemma4", got)
	}
}

func TestList_NonCatalogInstalledSnapsAreIgnored(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"gemma4"}},
		fakeSnaps{statuses: map[string]string{"gemma4": "active", "some-other-snap": "active"}},
	)

	got, _, err := svc.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v, want exactly one provider", got)
	}
}

func TestList_SortedByName(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"qwen3", "gemma4", "deepseek-r1"}},
		fakeSnaps{statuses: map[string]string{}},
	)

	got, _, err := svc.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantOrder := []string{"deepseek-r1", "gemma4", "qwen3"}
	for i, name := range wantOrder {
		if got[i].Name != name {
			t.Fatalf("got order %v, want %v", got, wantOrder)
		}
	}
}

func TestList_CatalogErrorFailsCommand(t *testing.T) {
	svc := NewService(
		fakeCatalog{err: errors.New("catalog unavailable")},
		fakeSnaps{},
	)

	_, _, err := svc.List(context.Background(), ListOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestList_SnapdErrorNeverBecomesAllNotInstalled(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"gemma4"}},
		fakeSnaps{err: errors.New("snapd unavailable")},
	)

	got, _, err := svc.List(context.Background(), ListOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got != nil {
		t.Fatalf("expected no partial output, got %+v", got)
	}
}

func TestList_CatalogWarningsArePropagated(t *testing.T) {
	svc := NewService(
		fakeCatalog{names: []string{"gemma4"}, warnings: []string{"using stale cache"}},
		fakeSnaps{statuses: map[string]string{}},
	)

	_, warnings, err := svc.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(warnings) != 1 || warnings[0] != "using stale cache" {
		t.Fatalf("got warnings %v", warnings)
	}
}
