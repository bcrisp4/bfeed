package web

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/bcrisp4/bfeed/internal/core"
)

// templateHTML is an alias for html/template.HTML.
// Entry content is already sanitised at ingest (invariant 1), so it is safe
// to pass it through as trusted HTML without re-escaping.
type templateHTML = template.HTML

//go:embed templates/*.gohtml
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Handler is the HTTP handler for the bfeed web UI.
type Handler struct {
	feeds   *core.FeedService
	entries *core.EntryService
	log     *slog.Logger
	tmpl    map[string]*template.Template
}

// New constructs a fully-routed http.Handler for the bfeed web UI.
func New(feeds *core.FeedService, entries *core.EntryService, log *slog.Logger) http.Handler {
	h := &Handler{feeds: feeds, entries: entries, log: log, tmpl: parseTemplates()}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", h.unread)
	mux.HandleFunc("GET /feeds", h.listFeeds)
	mux.HandleFunc("GET /feeds/{id}", h.feedEntries)
	mux.HandleFunc("GET /starred", h.starred)
	mux.HandleFunc("GET /entries/{id}", h.entry)
	mux.HandleFunc("POST /feeds", h.subscribe)
	mux.HandleFunc("POST /feeds/{id}/refresh", h.refresh)
	mux.HandleFunc("POST /feeds/{id}/delete", h.deleteFeed)
	mux.HandleFunc("POST /entries/{id}/read", h.toggleRead)
	mux.HandleFunc("POST /entries/{id}/star", h.toggleStar)
	mux.HandleFunc("POST /entries/{id}/delete", h.deleteEntry)
	return logging(log, mux)
}

func parseTemplates() map[string]*template.Template {
	// Each page = layout + its content template (layout calls "content").
	pages := map[string][]string{
		"entries": {"templates/layout.gohtml", "templates/entries.gohtml"},
		"entry":   {"templates/layout.gohtml", "templates/entry.gohtml"},
		"feeds":   {"templates/layout.gohtml", "templates/feeds.gohtml"},
	}
	out := map[string]*template.Template{}
	for name, files := range pages {
		out[name] = template.Must(template.ParseFS(templatesFS, files...))
	}
	// Fragment-only template for htmx row swaps (toggleRead, toggleStar).
	out["entryrow"] = template.Must(template.ParseFS(templatesFS, "templates/entries.gohtml"))
	return out
}

func logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		log.Info("http", "method", r.Method, "path", r.URL.Path)
	})
}
