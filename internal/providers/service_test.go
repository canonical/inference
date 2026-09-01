package providers

import (
	"context"
	"errors"
	"testing"
)

type fakeRegistry struct {
	definitions []Definition
	warnings    []string
	err         error
}

func (f fakeRegistry) List(context.Context) ([]Definition, []string, error) {
	return f.definitions, f.warnings, f.err
}

type fakeStatusSource struct {
	statuses map[string]string
	err      error
}

func (f fakeStatusSource) Statuses(context.Context) (map[string]string, error) {
	return f.statuses, f.err
}

func inferenceSnaps(names ...string) []Definition {
	definitions := make([]Definition, len(names))
	for i, name := range names {
		definitions[i] = Definition{Name: name, Type: TypeInferenceSnap}
	}
	return definitions
}

func testService(registry fakeRegistry, statuses fakeStatusSource) *Service {
	return NewService(registry, map[Type]StatusSource{TypeInferenceSnap: statuses})
}

func TestList_InstalledProviderGetsSnapdStatus(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("gemma4")},
		fakeStatusSource{statuses: map[string]string{"gemma4": "active"}},
	)

	got, warnings, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	want := []Provider{{Name: "gemma4", Type: TypeInferenceSnap, Status: "active"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestList_AbsentProviderIsNotInstalled(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("gemma4")},
		fakeStatusSource{statuses: map[string]string{}},
	)

	got, _, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Status != StatusNotInstalled {
		t.Fatalf("got %+v, want status %q", got, StatusNotInstalled)
	}
}

func TestList_UnknownSnapdStatusIsPreserved(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("gemma4")},
		fakeStatusSource{statuses: map[string]string{"gemma4": "some-future-status"}},
	)

	got, _, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got[0].Status != "some-future-status" {
		t.Fatalf("got status %q, want preserved value", got[0].Status)
	}
}

func TestList_EmptyStatusForMatchingProviderFails(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("gemma4")},
		fakeStatusSource{statuses: map[string]string{"gemma4": ""}},
	)

	_, _, err := svc.List(context.Background(), false)
	if err == nil {
		t.Fatal("expected error for empty status, got nil")
	}
}

func TestList_InstalledOnlyExcludesNotInstalled(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("gemma4", "qwen3")},
		fakeStatusSource{statuses: map[string]string{"gemma4": "active"}},
	)

	got, _, err := svc.List(context.Background(), true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "gemma4" {
		t.Fatalf("got %+v, want only gemma4", got)
	}
}

func TestList_NonRegistryStatusesAreIgnored(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("gemma4")},
		fakeStatusSource{statuses: map[string]string{"gemma4": "active", "some-other-snap": "active"}},
	)

	got, _, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v, want exactly one provider", got)
	}
}

func TestList_SortedByName(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("qwen3", "gemma4", "deepseek-r1")},
		fakeStatusSource{statuses: map[string]string{}},
	)

	got, _, err := svc.List(context.Background(), false)
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

func TestList_RegistryErrorFailsCommand(t *testing.T) {
	svc := testService(
		fakeRegistry{err: errors.New("registry unavailable")},
		fakeStatusSource{},
	)

	_, _, err := svc.List(context.Background(), false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestList_StatusSourceErrorNeverBecomesAllNotInstalled(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("gemma4")},
		fakeStatusSource{err: errors.New("status source unavailable")},
	)

	got, _, err := svc.List(context.Background(), false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got != nil {
		t.Fatalf("expected no partial output, got %+v", got)
	}
}

func TestList_RegistryWarningsArePropagated(t *testing.T) {
	svc := testService(
		fakeRegistry{definitions: inferenceSnaps("gemma4"), warnings: []string{"using stale cache"}},
		fakeStatusSource{statuses: map[string]string{}},
	)

	_, warnings, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(warnings) != 1 || warnings[0] != "using stale cache" {
		t.Fatalf("got warnings %v", warnings)
	}
}

func TestList_UsesStatusSourceForEachProviderType(t *testing.T) {
	const remoteAPI Type = "remote-api"
	svc := NewService(
		fakeRegistry{definitions: []Definition{
			{Name: "gemma4", Type: TypeInferenceSnap},
			{Name: "hosted", Type: remoteAPI},
		}},
		map[Type]StatusSource{
			TypeInferenceSnap: fakeStatusSource{statuses: map[string]string{"gemma4": "active"}},
			remoteAPI:         fakeStatusSource{statuses: map[string]string{"hosted": "configured"}},
		},
	)

	got, _, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Status != "active" || got[1].Status != "configured" {
		t.Fatalf("got %+v", got)
	}
}

func TestList_FailsWithoutStatusSourceForProviderType(t *testing.T) {
	svc := NewService(
		fakeRegistry{definitions: []Definition{{Name: "hosted", Type: "remote-api"}}},
		nil,
	)

	if _, _, err := svc.List(context.Background(), false); err == nil {
		t.Fatal("expected missing status source error")
	}
}

func TestList_RejectsDuplicateProviderNamesBeforeReadingStatuses(t *testing.T) {
	statuses := &countingStatusSource{}
	svc := NewService(
		fakeRegistry{definitions: []Definition{
			{Name: "duplicate", Type: TypeInferenceSnap},
			{Name: "duplicate", Type: "remote-api"},
		}},
		map[Type]StatusSource{
			TypeInferenceSnap: statuses,
			"remote-api":      statuses,
		},
	)

	if _, _, err := svc.List(context.Background(), false); err == nil {
		t.Fatal("expected duplicate provider error")
	}
	if statuses.calls != 0 {
		t.Fatalf("status source called %d times", statuses.calls)
	}
}

type countingStatusSource struct {
	calls int
}

func (s *countingStatusSource) Statuses(context.Context) (map[string]string, error) {
	s.calls++
	return nil, nil
}
