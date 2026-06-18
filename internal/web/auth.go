package web

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// authMiddleware protects routes when user/pass are configured.
//
// Two ways in:
//  1. Signed session cookie (from form login at /login).
//  2. HTTP Basic auth header (for curl, scripts, healthz tooling).
//
// When both creds are empty, the middleware is a no-op — preserves the
// "just docker-compose up" local-dev experience.
//
// Failure mode depends on the client: browsers (Accept includes text/html)
// get redirected to /login?next=…; everything else gets a plain 401 so
// scripts and curl behave the same as before.
func authMiddleware(user, pass string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if user == "" && pass == "" {
			return next
		}
		userB := []byte(user)
		passB := []byte(pass)
		key := sessionKey(pass)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Cookie path.
			if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
				if _, perr := parseSessionToken(c.Value, key); perr == nil {
					next.ServeHTTP(w, r)
					return
				}
			}
			// 2. Basic auth path.
			if u, p, ok := r.BasicAuth(); ok &&
				subtle.ConstantTimeCompare([]byte(u), userB) == 1 &&
				subtle.ConstantTimeCompare([]byte(p), passB) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			// 3. Reject.
			if isBrowserRequest(r) {
				nextPath := r.URL.RequestURI()
				if nextPath == "" {
					nextPath = "/"
				}
				http.Redirect(w, r,
					"/login?next="+url.QueryEscape(nextPath),
					http.StatusSeeOther)
				return
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="Chinese Cards", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}
}

// isBrowserRequest is a best-effort sniff: GETs that accept HTML get the
// friendlier redirect; everything else falls through to the 401 path so
// scripts/curl/HTMX behave predictably.
func isBrowserRequest(r *http.Request) bool {
	if r.Header.Get("HX-Request") == "true" {
		// HTMX swap — must not redirect; it would inject an entire <html>
		// document into a fragment slot. Let the XHR get 401 and the user
		// will hit /login on the next full navigation.
		return false
	}
	if r.Method != http.MethodGet {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
