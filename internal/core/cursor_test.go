package core

import (
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{Key: 1_700_000_000, ID: 42}
	got := DecodeCursor(EncodeCursor(c))
	if got == nil || got.Key != c.Key || got.ID != c.ID {
		t.Fatalf("round-trip failed: %+v", got)
	}
	if DecodeCursor("!!not-base64!!") != nil {
		t.Fatal("malformed cursor should decode to nil")
	}
}

func TestCursorKey(t *testing.T) {
	pub := time.Unix(1_700_000_000, 0)
	read := time.Unix(1_700_009_999, 0)
	e := &Entry{PublishedAt: pub, ReadAt: &read}

	if got := CursorKey(e, OrderPublishedDesc); got != pub.Unix() {
		t.Fatalf("published order key = %d, want %d", got, pub.Unix())
	}
	if got := CursorKey(e, OrderReadAtDesc); got != read.Unix() {
		t.Fatalf("read-at order key = %d, want %d", got, read.Unix())
	}
	// Read-at order falls back to published when the entry is unread (ReadAt nil).
	e.ReadAt = nil
	if got := CursorKey(e, OrderReadAtDesc); got != pub.Unix() {
		t.Fatalf("unread read-at key = %d, want published %d", got, pub.Unix())
	}
}
