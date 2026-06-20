package web

import "net/http"

// chrome holds per-request presentation prefs shared by every full page render.
// Theme "" means "follow the OS" (the layout then omits data-theme so the CSS
// prefers-color-scheme baseline applies).
type chrome struct {
	Theme     string // "", "light", "sepia", "dark"
	Summaries string // "show" | "hide"
	Width     string // "comfortable" | "wide"
	Active    string // active nav key
}

func cookieOr(r *http.Request, name, def string, allowed ...string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return def
	}
	for _, a := range allowed {
		if c.Value == a {
			return c.Value
		}
	}
	return def
}

// chromeFor reads the pref cookies, validating each against its closed enum.
func (h *Handler) chromeFor(r *http.Request, active string) chrome {
	theme := cookieOr(r, "bfeed_theme", "system", "system", "light", "sepia", "dark")
	if theme == "system" {
		theme = ""
	}
	return chrome{
		Theme:     theme,
		Summaries: cookieOr(r, "bfeed_summary", "show", "show", "hide"),
		Width:     cookieOr(r, "bfeed_width", "comfortable", "comfortable", "wide"),
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
