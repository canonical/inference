package snapcatalog

import (
	"testing"
	"time"
)

func TestValidateDocumentRejectsDuplicatesAndUnsortedEntries(t *testing.T) {
	now := time.Now()
	duplicate := testDocument(now)
	duplicate.Entries[1].SnapName = duplicate.Entries[0].SnapName
	if err := ValidateDocument(duplicate); err == nil {
		t.Fatal("expected duplicate name error")
	}

	unsorted := testDocument(now)
	unsorted.Entries[0], unsorted.Entries[1] = unsorted.Entries[1], unsorted.Entries[0]
	if err := ValidateDocument(unsorted); err == nil {
		t.Fatal("expected unsorted entry error")
	}
}
