package core

import "testing"

func TestFeedDisplayTitle(t *testing.T) {
	cases := []struct{ user, title, want string }{
		{"", "Auto Title", "Auto Title"},
		{"My Name", "Auto Title", "My Name"},
		{"   ", "Auto Title", "Auto Title"}, // blank override ignored
	}
	for _, c := range cases {
		f := &Feed{UserTitle: c.user, Title: c.title}
		if got := f.DisplayTitle(); got != c.want {
			t.Errorf("DisplayTitle(user=%q,title=%q)=%q want %q", c.user, c.title, got, c.want)
		}
	}
}

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
