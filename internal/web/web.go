package web

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/CAFxX/httpcompression"

	"github.com/bcrisp4/bfeed/internal/core"
)

// compressibleTypes is the allowlist of response content types worth
// compressing. Everything else (notably already-compressed woff2 fonts and
// images) is served as-is — re-compressing them only burns CPU for no gain.
var compressibleTypes = []string{
	"text/html",
	"text/css",
	"text/javascript",
	"application/javascript",
	"application/json",
	"image/svg+xml",
}

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
	cats    *core.CategoryService
	search  *core.SearchService
	log     *slog.Logger
	tmpl    map[string]*template.Template
}

// New constructs a fully-routed http.Handler for the bfeed web UI.
func New(feeds *core.FeedService, entries *core.EntryService, cats *core.CategoryService, search *core.SearchService, log *slog.Logger) http.Handler {
	h := &Handler{feeds: feeds, entries: entries, cats: cats, search: search, log: log, tmpl: parseTemplates()}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", cacheStatic(http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", h.unread)
	mux.HandleFunc("GET /feeds", h.listFeeds)
	mux.HandleFunc("GET /feeds/{id}", h.feedEntries)
	mux.HandleFunc("GET /starred", h.starred)
	mux.HandleFunc("GET /history", h.history)
	mux.HandleFunc("GET /categories", h.categoriesIndex)
	mux.HandleFunc("GET /categories/none", h.uncategorisedEntries)
	mux.HandleFunc("GET /categories/{id}", h.categoryEntries)
	mux.HandleFunc("GET /entries/{id}", h.entry)
	mux.HandleFunc("POST /feeds", h.subscribe)
	mux.HandleFunc("POST /feeds/{id}/refresh", h.refresh)
	mux.HandleFunc("POST /feeds/{id}/mark-read", h.markFeedRead)
	mux.HandleFunc("POST /feeds/{id}/delete", h.deleteFeed)
	mux.HandleFunc("POST /feeds/{id}/category", h.setFeedCategory)
	mux.HandleFunc("POST /feeds/{id}/full-content", h.setFeedFullContent)
	mux.HandleFunc("POST /categories", h.createCategory)
	mux.HandleFunc("POST /categories/{id}/rename", h.renameCategory)
	mux.HandleFunc("POST /categories/{id}/delete", h.deleteCategory)
	mux.HandleFunc("POST /entries/{id}/read", h.toggleRead)
	mux.HandleFunc("POST /entries/{id}/star", h.toggleStar)
	mux.HandleFunc("POST /entries/{id}/delete", h.deleteEntry)
	mux.HandleFunc("GET /search", h.searchHandler)
	mux.HandleFunc("GET /settings", h.settings)
	mux.HandleFunc("POST /settings", h.saveSettings)
	// gzip/brotli text responses (HTML, CSS, JS) on the fly for clients that
	// accept it — the biggest low-bandwidth win, and it covers dynamic HTML
	// (every no-store page) which precompressed-static serving would miss.
	compress, err := httpcompression.DefaultAdapter(httpcompression.ContentTypes(compressibleTypes, false))
	if err != nil {
		// Only static, valid options are passed, so this can never fail at runtime.
		panic("web: compression adapter: " + err.Error())
	}
	return compress(logging(log, noStore(mux)))
}

func parseTemplates() map[string]*template.Template {
	// Partials every page includes: the shell, nav, and shared icon defines.
	// Kept in one place so a new partial can't be silently omitted from a page
	// (the bug _icons.gohtml itself was introduced to fix).
	common := []string{"templates/layout.gohtml", "templates/_nav.gohtml", "templates/_icons.gohtml"}
	// Each page = common + its content template(s) (layout calls "content").
	pages := map[string][]string{
		"entries":    {"templates/entries.gohtml", "templates/rows.gohtml"},
		"entry":      {"templates/entry.gohtml"},
		"feeds":      {"templates/feeds.gohtml"},
		"categories": {"templates/categories.gohtml"},
		"search":     {"templates/search.gohtml", "templates/rows.gohtml"},
		"settings":   {"templates/settings.gohtml"},
	}
	// asset injects a fingerprinted (cache-busting) URL for a static asset, so
	// layout.gohtml can reference CSS/JS by a versioned URL — see assetURL.
	funcs := template.FuncMap{"asset": assetURL}
	out := map[string]*template.Template{}
	for name, files := range pages {
		all := append(append([]string{}, common...), files...)
		out[name] = template.Must(template.New(name).Funcs(funcs).ParseFS(templatesFS, all...))
	}
	// Fragment-only template for htmx row swaps (toggleRead, toggleStar).
	out["entryrow"] = template.Must(template.ParseFS(templatesFS, "templates/rows.gohtml", "templates/_icons.gohtml"))
	return out
}

// noStore marks dynamic responses uncacheable so the browser refetches list
// pages on Back/Forward instead of restoring a stale DOM (an opened entry is
// marked read server-side; a bfcached page would still show it unread). The
// static handler sets its own Cache-Control inside cacheStatic, which overrides
// this for /static/ assets.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// cacheStatic sets cache headers on embedded static assets. Fonts are
// content-stable (a given file name always holds the same face), so they are
// cached immutably for a year. CSS/JS can change between releases and carry no
// content hash in their names, so they get a short cache and are re-fetched
// soon after a deploy rather than served stale.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".woff2"):
			// Fonts are content-stable: a given file name always holds the same face.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case r.URL.Query().Has("v"):
			// Fingerprinted asset: the ?v= hash changes when the bytes change, so
			// this exact URL is the version and is safe to cache forever (assetURL).
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			// Un-fingerprinted direct hit: may change between releases, so
			// revalidate soon rather than risk serving stale CSS/JS.
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		next.ServeHTTP(w, r)
	})
}

func logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		log.Info("http", "method", r.Method, "path", r.URL.Path)
	})
}
