package core

import "testing"

func TestEntryStatusValid(t *testing.T) {
	if !StatusUnread.Valid() || !StatusRead.Valid() {
		t.Fatal("known statuses must be valid")
	}
	if EntryStatus("bogus").Valid() {
		t.Fatal("unknown status must be invalid")
	}
}

func TestDefaultUserID(t *testing.T) {
	if DefaultUserID != 1 {
		t.Fatalf("DefaultUserID = %d, want 1", DefaultUserID)
	}
}
