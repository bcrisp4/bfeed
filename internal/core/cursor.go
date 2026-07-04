package core

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// CursorKey returns the unix-seconds value of an entry's active keyset order
// column. This defines what Cursor.Key means per Order; the sqlite store and the
// MemStore fake must derive it identically or pagination page boundaries diverge,
// so both call this one function rather than reimplementing the switch.
func CursorKey(e *Entry, ord Order) int64 {
	if ord == OrderReadAtDesc && e.ReadAt != nil {
		return e.ReadAt.Unix()
	}
	return e.PublishedAt.Unix()
}

// EncodeCursor serialises a keyset position as base64("<key>:<id>"),
// where key is the active order column in unix seconds.
func EncodeCursor(c Cursor) string {
	raw := strconv.FormatInt(c.Key, 10) + ":" + strconv.FormatInt(int64(c.ID), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses an EncodeCursor value; returns nil if malformed.
func DecodeCursor(s string) *Cursor {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(string(b), ":", 2)
	if len(parts) != 2 {
		return nil
	}
	key, err1 := strconv.ParseInt(parts[0], 10, 64)
	id, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return nil
	}
	return &Cursor{Key: key, ID: ID(id)}
}
