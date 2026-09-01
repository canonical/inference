package snapcatalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 2

type Document struct {
	SchemaVersion int       `json:"schema_version"`
	FetchedAt     time.Time `json:"fetched_at"`
	Entries       []Entry   `json:"entries"`
}

func DecodeDocument(data []byte) (Document, error) {
	var document Document
	if err := json.Unmarshal(data, &document); err != nil {
		return Document{}, err
	}
	if err := ValidateDocument(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func EncodeDocument(document Document) ([]byte, error) {
	if err := ValidateDocument(document); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func ValidateDocument(document Document) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported snap catalog schema version %d", document.SchemaVersion)
	}
	if document.FetchedAt.IsZero() {
		return fmt.Errorf("snap catalog fetch timestamp is missing")
	}
	if len(document.Entries) == 0 {
		return fmt.Errorf("resolved snap catalog contains no entries")
	}

	names := make(map[string]bool, len(document.Entries))
	repos := make(map[string]bool, len(document.Entries))
	for i, entry := range document.Entries {
		if err := ValidateSnapName(entry.SnapName); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		if strings.TrimSpace(entry.Model) == "" {
			return fmt.Errorf("entry %q has no model name", entry.SnapName)
		}
		if !repositoryPattern.MatchString(entry.Repository) {
			return fmt.Errorf("entry %q has invalid repository %q", entry.SnapName, entry.Repository)
		}
		if names[entry.SnapName] {
			return fmt.Errorf("duplicate snap name %q", entry.SnapName)
		}
		if repos[entry.Repository] {
			return fmt.Errorf("duplicate repository %q", entry.Repository)
		}
		names[entry.SnapName] = true
		repos[entry.Repository] = true
	}
	if !sort.SliceIsSorted(document.Entries, func(i, j int) bool {
		return document.Entries[i].SnapName < document.Entries[j].SnapName
	}) {
		return fmt.Errorf("resolved snap catalog entries are not sorted by snap name")
	}
	return nil
}
