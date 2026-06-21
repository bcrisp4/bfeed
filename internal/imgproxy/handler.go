package imgproxy

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/bcrisp4/bfeed/internal/core"
)

// Handler serves GET /img?u=<url>&s=<sig>: verify signature, fetch through the
// SSRF-guarded Fetcher, and serve only image/* with a long browser cache.
type Handler struct {
	fetcher core.Fetcher
	signer  *Signer
	log     *slog.Logger
}

func New(fetcher core.Fetcher, signer *Signer, log *slog.Logger) *Handler {
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
	resp, err := h.fetcher.Fetch(r.Context(), core.FetchRequest{URL: raw})
	if err != nil {
		h.log.Debug("image proxy fetch", "url", raw, "error", err)
		http.Error(w, "fetch failed", http.StatusBadGateway)
		return
	}
	if resp.Status != http.StatusOK {
		http.Error(w, "upstream status", http.StatusBadGateway)
		return
	}
	if !strings.HasPrefix(strings.ToLower(resp.ContentType), "image/") {
		http.Error(w, "not an image", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", resp.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(resp.Body)
}
