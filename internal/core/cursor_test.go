package core

import "testing"

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
