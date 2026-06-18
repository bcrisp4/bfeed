package core

import (
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{PublishedAt: time.Unix(1_700_000_000, 0).UTC(), ID: 42}
	got := DecodeCursor(EncodeCursor(c))
	if got == nil || !got.PublishedAt.Equal(c.PublishedAt) || got.ID != c.ID {
		t.Fatalf("round-trip failed: %+v", got)
	}
	if DecodeCursor("!!not-base64!!") != nil {
		t.Fatal("malformed cursor should decode to nil")
	}
}
