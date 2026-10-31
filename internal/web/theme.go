package web

import (
	"net/http"
	"net/url"
)

const themeCookie = "theme"

// The theme is a plain cookie that the server turns into data-theme on <html>
// before the first paint, so there is no flash and no inline script. app.js
// writes the same cookie when the button is clicked; without JS the button is
// a link to /theme that does it on the server.
func validTheme(v string) bool { return v == "light" || v == "dark" || v == "auto" }

func themeOf(r *http.Request) string {
	if c, err := r.Cookie(themeCookie); err == nil && (c.Value == "light" || c.Value == "dark") {
		return c.Value
	}
	return ""
}

// themeNext is what the button switches to: auto, then light, then dark.
func themeNext(cur string) string {
	switch cur {
	case "":
		return "light"
	case "light":
		return "dark"
	}
	return "auto"
}

// themeWriter carries the visitor's theme from the middleware to render,
// which builds pages from many handlers that never see the request.
type themeWriter struct {
	http.ResponseWriter
	theme string
}

func (w *themeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func themeFor(w http.ResponseWriter) string {
	if tw, ok := w.(*themeWriter); ok {
		return tw.theme
	}
	return ""
}

func (s *Server) themeSet(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query().Get("set")
	if !validTheme(v) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	c := &http.Cookie{Name: themeCookie, Value: v, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode, Secure: s.Secure}
	if v == "auto" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
	back := "/"
	if u, err := url.Parse(r.Referer()); err == nil && u.Host == r.Host && u.Path != "" && u.Path != "/theme" {
		back = u.Path
		if u.RawQuery != "" {
			back += "?" + u.RawQuery
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
