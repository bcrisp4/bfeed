package web

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/CAFxX/httpcompression"

	"github.com/bcrisp4/bfeed/internal/core"
)

// HTTPMetrics is the consumer-owned port for HTTP request instrumentation —
// satisfied by *observability.Metrics (wired in cmd/bfeed) without web ever
// importing prometheus. A nil HTTPMetrics disables instrumentation entirely.
type HTTPMetrics interface {
	HTTPRequest(route, method string, status int, d time.Duration)
}

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
	feeds      *core.FeedService
	entries    *core.EntryService
	cats       *core.CategoryService
	search     *core.SearchService
	log        *slog.Logger
	tmpl       map[string]*template.Template
	imgRewrite func(string) string // nil = image proxy disabled
	errorLimit int                 // a feed with error_count >= this is flagged stalled in the UI
	busy       *inflightSet
	bgOps      sync.WaitGroup // tracks in-flight background subscribe/refresh goroutines
	router     http.Handler   // the composed middleware chain; ServeHTTP delegates here
}

// New constructs a fully-routed *Handler for the bfeed web UI. The returned
// value implements http.Handler; callers that need to drain in-flight
// background feed ops on shutdown hold onto the concrete type to call Drain.
func New(feeds *core.FeedService, entries *core.EntryService, cats *core.CategoryService, search *core.SearchService, log *slog.Logger, imgHandler http.Handler, imgRewrite func(string) string, errorLimit int, expectedHost string, metrics HTTPMetrics, ready func(context.Context) error) *Handler {
	h := &Handler{feeds: feeds, entries: entries, cats: cats, search: search, log: log, tmpl: parseTemplates(), imgRewrite: imgRewrite, errorLimit: errorLimit, busy: newInflightSet()}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", cacheStatic(http.FileServer(http.FS(staticFS))))
	if imgHandler != nil {
		mux.Handle("GET /img", imgHandler)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", readyzHandler(ready))
	mux.HandleFunc("GET /{$}", h.unread)
	mux.HandleFunc("GET /feeds", h.listFeeds)
	mux.HandleFunc("GET /feeds/{id}", h.feedEntries)
	mux.HandleFunc("GET /feeds/{id}/row", h.feedRow)
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
	mux.HandleFunc("GET /feeds/{id}/edit", h.feedEditForm)
	mux.HandleFunc("POST /feeds/{id}", h.editFeed)
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
	var inner http.Handler = mux
	if metrics != nil {
		inner = instrument(metrics, mux)
	}
	h.router = compress(logging(log, hostGuard(expectedHost, securityHeaders(noStore(inner)))))
	return h
}

// readyzHandler builds the GET /readyz handler. A nil ready probe (no
// readiness check configured) always reports ready; otherwise the probe runs
// with a bounded timeout so a hung dependency can't wedge the endpoint.
func readyzHandler(ready func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ready == nil {
			_, _ = w.Write([]byte("ok"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			http.Error(w, "unready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}
}

// httpMethods is the closed allowlist of method labels recorded by instrument
// — anything else (WebDAV verbs, typos, probes) collapses to "other" so the
// method label can't blow up metric cardinality.
var httpMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodPost:    true,
	http.MethodHead:    true,
	http.MethodPut:     true,
	http.MethodDelete:  true,
	http.MethodOptions: true,
	http.MethodPatch:   true,
}

// methodLabel maps a request method to its metric label, collapsing anything
// outside the allowlist to "other".
func methodLabel(method string) string {
	if httpMethods[method] {
		return method
	}
	return "other"
}

// statusRecorder wraps a ResponseWriter to capture the status code the
// handler actually sent, defaulting to 200 (the http.ResponseWriter default)
// when the handler writes a body without ever calling WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (sr *statusRecorder) WriteHeader(status int) {
	if !sr.written {
		sr.status = status
		sr.written = true
	}
	sr.ResponseWriter.WriteHeader(status)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if !sr.written {
		sr.status = http.StatusOK
		sr.written = true
	}
	return sr.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying ResponseWriter for http.ResponseController
// (e.g. Flush/Hijack/SetReadDeadline callers that type-assert through it).
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// instrument records per-request HTTP metrics. It wraps the mux directly (the
// innermost layer of the chain) so the recorded route/status reflect exactly
// what the mux dispatched and returned, before hostGuard/securityHeaders/
// noStore/compress/logging touch the response.
func instrument(m HTTPMetrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(sr, r)
		// Go's ServeMux sets r.Pattern in place during ServeHTTP; read it only
		// after the handler has run. An unmatched request (404) leaves it empty.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		status := sr.status
		if !sr.written {
			status = http.StatusOK
		}
		m.HTTPRequest(route, methodLabel(r.Method), status, time.Since(start))
	})
}

// ServeHTTP delegates to the composed middleware chain so *Handler satisfies
// http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.router.ServeHTTP(w, r)
}

// Drain waits for in-flight background feed-op goroutines (subscribe/refresh) to
// finish, or for ctx to expire. It returns true if they drained, false if ctx
// expired first (some ops may still be running). Called between srv.Shutdown and
// db.Close so a graceful shutdown never closes the DB underneath a mid-flight
// ResolveAndIngest/Refresh write.
func (h *Handler) Drain(ctx context.Context) bool {
	done := make(chan struct{})
	go func() { h.bgOps.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
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
		"feeds":      {"templates/feeds.gohtml", "templates/rows_feed.gohtml"},
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
	// Fragment-only template for htmx feed row swaps (refresh, feedRow).
	out["feedrow"] = template.Must(template.ParseFS(templatesFS, "templates/rows_feed.gohtml", "templates/_icons.gohtml"))
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

// contentSecurityPolicy locks the app pages down so a bluemonday bypass (parser
// differential, future policy regression, or an entry sanitised under an older
// policy and trusted forever) can't execute script on the bfeed origin. Feed
// images may be remote when the image proxy is disabled (over http on a
// plain-http tailnet deploy or https), hence img-src http: https: data:;
// everything else is self-only. The one inline script is externalised to
// /static/app.js so no 'unsafe-inline' is needed; htmx 2.0.4 needs no eval and
// its inline indicator <style> is disabled via the htmx-config meta in layout.
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' http: https: data:; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'; " +
	"form-action 'self'"

// securityHeaders adds defence-in-depth headers to every response. The image
// proxy sets its own stricter CSP on /img afterwards (same override pattern
// noStore relies on), so proxied images keep default-src 'none'; sandbox.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// no-referrer also covers entries persisted before rel=noreferrer sanitising.
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// hostGuard rejects requests whose Host header doesn't match the app's own
// origin, defeating DNS-rebinding: an attacker page that re-resolves its name to
// the bfeed tailnet IP still sends its own name in Host, so its same-origin
// fetches (and cross-origin mutating POSTs) are refused. The comparison is
// case-insensitive because hostnames are. Only the /healthz and /readyz paths
// are exempt — so the container HEALTHCHECK/readiness probe (GET
// 127.0.0.1/healthz|/readyz) works without opening the rest of the app to a
// same-machine attacker who can spoof a loopback Host on a mutating endpoint.
// An empty expectedHost disables the check (used by tests).
func hostGuard(expectedHost string, next http.Handler) http.Handler {
	expected := normalizeAuthority(expectedHost)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expectedHost != "" && r.URL.Path != "/healthz" && r.URL.Path != "/readyz" && normalizeAuthority(r.Host) != expected {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// normalizeAuthority lowercases a Host header / URL authority and drops a
// default HTTP(S) port, so "Host", "host:80", and "host:443" compare equal.
// Hostnames are case-insensitive, and clients/proxies may include or elide the
// default port; the DNS-rebinding guard only cares about hostname identity.
func normalizeAuthority(hostport string) string {
	hostport = strings.ToLower(hostport)
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport // no port present (or malformed) — compare as-is
	}
	if port == "80" || port == "443" {
		return host
	}
	return net.JoinHostPort(host, port)
}
