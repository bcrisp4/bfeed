package imgproxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bcrisp4/bfeed/internal/core"
)

// maxImageBytes caps a proxied image. Streaming means peak memory is a fixed copy
// buffer regardless, but the cap still bounds how much we'll relay for one request.
const maxImageBytes = 10 << 20

// StreamFetcher fetches through the SSRF-guarded client and returns the body as a
// stream (not a buffered []byte), so the proxy never holds a whole image in memory.
// Consumer-owned interface (only imgproxy needs streaming); *fetch.Client satisfies it.
type StreamFetcher interface {
	FetchStream(ctx context.Context, req core.FetchRequest) (*core.FetchStreamResponse, error)
}

// Handler serves GET /img?u=<url>&s=<sig>: verify signature, fetch through the
// SSRF-guarded StreamFetcher, and stream only image/* with a long browser cache.
type Handler struct {
	fetcher StreamFetcher
	signer  *Signer
	log     *slog.Logger
}

func New(fetcher StreamFetcher, signer *Signer, log *slog.Logger) *Handler {
	return &Handler{fetcher: fetcher, signer: signer, log: log}
}

var _ http.Handler = (*Handler)(nil)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	sig := r.URL.Query().Get("s")
	if raw == "" || sig == "" {
		http.Error(w, "missing params", http.StatusBadRequest)
		return
	}
	if !h.signer.Verify(raw, sig) {
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	resp, err := h.fetcher.FetchStream(r.Context(), core.FetchRequest{URL: raw})
	if err != nil {
		h.log.Debug("image proxy fetch", "url", raw, "error", err)
		http.Error(w, "fetch failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close() // also releases the fetcher's per-host token
	if resp.Status != http.StatusOK {
		http.Error(w, "upstream status", http.StatusBadGateway)
		return
	}
	if !strings.HasPrefix(strings.ToLower(resp.ContentType), "image/") {
		http.Error(w, "not an image", http.StatusBadGateway)
		return
	}
	// Reject an over-cap image BEFORE writing any headers: the response carries an
	// immutable year-long cache, so a truncated body must never reach the client as
	// a "complete" 200. A lying/absent Content-Length is caught while streaming below.
	if resp.ContentLength > maxImageBytes {
		http.Error(w, "image too large", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", resp.ContentType)
	// Long-lived browser cache. This Set overrides the no-store header the web
	// layer's noStore middleware applies to every dynamic response (the last write
	// before the body wins), so proxied images stay cacheable.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Defence in depth: image/svg+xml passes the image/ prefix check but is active
	// content. Served from our own origin and opened directly (e.g. "open image in
	// new tab"), an embedded script would execute in our origin. The sandbox +
	// locked-down policy neutralises script execution and any subresource load,
	// while still letting the bytes render as an inert image.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if resp.ContentLength >= 0 { // known and (checked above) within cap
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}

	// Read one byte past the cap so an image that streams over it (unknown/lying
	// Content-Length) is detected. On over-cap OR any mid-copy read error, abort the
	// connection (ErrAbortHandler) instead of returning a normal 200 — a clean 200
	// would let the browser cache the truncated bytes, immutable, for a year.
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil || n > maxImageBytes {
		panic(http.ErrAbortHandler)
	}
}
