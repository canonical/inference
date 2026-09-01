package catalog

import "testing"

func TestParseRemoteCatalog_Empty(t *testing.T) {
	if _, err := ParseRemoteCatalog([]byte(`{"repositories":{}}`)); err == nil {
		t.Fatal("expected error for empty catalog, got nil")
	}
}

func TestParseRemoteCatalog_Malformed(t *testing.T) {
	if _, err := ParseRemoteCatalog([]byte(`not json`)); err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestPublicRepositories_FiltersNonPublic(t *testing.T) {
	cat := RemoteCatalog{Repositories: map[string]RemoteRepository{
		"a": {FullName: "canonical/a-snap", Visibility: "public"},
		"b": {FullName: "canonical/b-snap", Visibility: "private"},
	}}

	repos := PublicRepositories(cat)
	if len(repos) != 1 || repos[0].FullName != "canonical/a-snap" {
		t.Fatalf("got %+v", repos)
	}
}

func TestPublicRepositories_OrderIndependentOfMapKeys(t *testing.T) {
	cat1 := RemoteCatalog{Repositories: map[string]RemoteRepository{
		"z-key": {FullName: "canonical/a-snap", Visibility: "public"},
		"a-key": {FullName: "canonical/b-snap", Visibility: "public"},
	}}
	cat2 := RemoteCatalog{Repositories: map[string]RemoteRepository{
		"a-key": {FullName: "canonical/b-snap", Visibility: "public"},
		"z-key": {FullName: "canonical/a-snap", Visibility: "public"},
	}}

	got1 := PublicRepositories(cat1)
	got2 := PublicRepositories(cat2)
	if len(got1) != 2 || len(got2) != 2 {
		t.Fatalf("got %+v and %+v", got1, got2)
	}
	if got1[0].FullName != got2[0].FullName || got1[1].FullName != got2[1].FullName {
		t.Fatalf("order depends on map keys: %+v vs %+v", got1, got2)
	}
}

func TestValidateSnapName(t *testing.T) {
	valid := []string{"gemma4", "glm-4-7-flash", "nomic-embed-text-v1-5", "qwen3-8"}
	for _, name := range valid {
		if err := ValidateSnapName(name); err != nil {
			t.Errorf("expected %q to be valid, got %v", name, err)
		}
	}

	invalid := []string{"", "Gemma4", "gemma_4", "-gemma4", "gemma4-", "gemma--4"}
	for _, name := range invalid {
		if err := ValidateSnapName(name); err == nil {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}

func TestParseSnapcraftName_UsesRootName(t *testing.T) {
	yaml := []byte(`
name: glm-4-7-flash
base: core24
summary: something
`)
	name, err := ParseSnapcraftName(yaml)
	if err != nil {
		t.Fatalf("ParseSnapcraftName: %v", err)
	}
	if name != "glm-4-7-flash" {
		t.Fatalf("got %q, want glm-4-7-flash", name)
	}
}

func TestParseSnapcraftName_MissingName(t *testing.T) {
	if _, err := ParseSnapcraftName([]byte(`base: core24`)); err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
}

func TestParseSnapcraftName_InvalidYAML(t *testing.T) {
	if _, err := ParseSnapcraftName([]byte("name: [unterminated")); err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestParseSnapcraftName_InvalidSnapName(t *testing.T) {
	if _, err := ParseSnapcraftName([]byte(`name: Not_Valid`)); err == nil {
		t.Fatal("expected error for invalid snap name, got nil")
	}
}
