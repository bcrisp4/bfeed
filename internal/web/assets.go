package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
)

// assetHashes maps a static asset's logical name (relative to static/, e.g.
// "app.css") to a short content hash, computed once at startup from the embedded
// bytes. Templates reference assets via assetURL so a changed asset gets a new
// URL and the browser cannot serve a stale copy — fingerprinting with no build
// step (the hash is derived from the bytes already baked into the binary).
var assetHashes = computeAssetHashes()

func computeAssetHashes() map[string]string {
	m := map[string]string{}
	err := fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		m[strings.TrimPrefix(p, "static/")] = hex.EncodeToString(sum[:])[:12]
		return nil
	})
	if err != nil {
		// Assets are embedded at build time, so this walk cannot fail at runtime.
		// If it somehow does, fail fast rather than silently fall back to
		// un-fingerprinted URLs that would reintroduce stale-asset caching.
		panic("web: hashing static assets: " + err.Error())
	}
	return m
}

// assetURL returns the cache-busting URL for a static asset referenced from a
// template, e.g. assetURL("app.css") -> "/static/app.css?v=<hash>". A request
// carrying that ?v= is served immutable (see cacheStatic). Unknown names fall
// back to the bare path (still served, just not fingerprinted).
func assetURL(name string) string {
	if h, ok := assetHashes[name]; ok {
		return "/static/" + name + "?v=" + h
	}
	return "/static/" + name
}
