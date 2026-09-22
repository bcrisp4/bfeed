package web

import (
	"net/http"

	"github.com/bcrisp4/bfeed/internal/core"
)

func (h *Handler) exportOPML(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.feeds.List(r.Context(), uid)
	if err != nil {
		h.log.Error("list feeds for OPML export", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	categories, err := h.cats.List(r.Context(), uid)
	if err != nil {
		h.log.Error("list categories for OPML export", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data, err := core.MarshalOPML(feeds, categories)
	if err != nil {
		h.log.Error("marshal OPML export", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="bfeed-feeds.opml"`)
	if _, err := w.Write(data); err != nil {
		h.log.Error("write OPML export", "error", err)
	}
}
