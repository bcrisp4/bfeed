package core

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// EncodeCursor serialises a keyset position as base64("<unixsecs>:<id>").
func EncodeCursor(c Cursor) string {
	raw := strconv.FormatInt(c.PublishedAt.UTC().Unix(), 10) + ":" + strconv.FormatInt(int64(c.ID), 10)
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
	sec, err1 := strconv.ParseInt(parts[0], 10, 64)
	id, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return nil
	}
	return &Cursor{PublishedAt: time.Unix(sec, 0).UTC(), ID: ID(id)}
}
