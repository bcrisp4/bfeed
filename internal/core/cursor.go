package core

import (
	"encoding/base64"
	"strconv"
	"strings"
)

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
