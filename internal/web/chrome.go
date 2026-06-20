package web

import "net/http"

// Closed enums for the three presentation preferences. Shared by the read path
// (chromeFor, from cookies) and the write path (saveSettings, from the form) so
// the accepted set cannot drift between them.
var (
	prefThemes    = []string{"system", "light", "sepia", "dark"}
	prefSummaries = []string{"show", "hide"}
	prefWidths    = []string{"comfortable", "wide"}
)

// chrome holds per-request presentation prefs shared by every full page render.
// Theme "" means "follow the OS" (the layout then omits data-theme so the CSS
// prefers-color-scheme baseline applies).
type chrome struct {
	Theme     string // "", "light", "sepia", "dark"
	Summaries string // "show" | "hide"
	Width     string // "comfortable" | "wide"
	Active    string // active nav key
}

// allowedOr returns v when it is one of allowed, else def.
func allowedOr(v, def string, allowed []string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return def
}

func cookieOr(r *http.Request, name, def string, allowed []string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return def
	}
	return allowedOr(c.Value, def, allowed)
}

// chromeFor reads the pref cookies, validating each against its closed enum.
func (h *Handler) chromeFor(r *http.Request, active string) chrome {
	theme := cookieOr(r, "bfeed_theme", "system", prefThemes)
	if theme == "system" {
		theme = ""
	}
	return chrome{
		Theme:     theme,
		Summaries: cookieOr(r, "bfeed_summary", "show", prefSummaries),
		Width:     cookieOr(r, "bfeed_width", "comfortable", prefWidths),
		Active:    active,
	}
}

func setPrefCookie(w http.ResponseWriter, name, value string) {
	// Secure is intentionally omitted: bfeed may be served over plain HTTP on a
	// tailnet, where forcing Secure would silently drop these preference cookies.
	// These are non-sensitive UI prefs and HttpOnly is set.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure deliberately unset for plain-HTTP tailnet deploys
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   31536000,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
