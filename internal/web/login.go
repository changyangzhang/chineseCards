package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type loginPageData struct {
	NextPath string // where to send the user after a successful login
	Error    string
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if readSessionCookie(r, s.cfg.BasicPass) != "" {
		http.Redirect(w, r, safeNextPath(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	s.renderer.Render(w, "login", loginPageData{
		NextPath: safeNextPath(r.URL.Query().Get("next")),
	})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	next := safeNextPath(r.FormValue("next"))

	// Constant-time compare on both fields to avoid timing oracles.
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(s.cfg.BasicUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.BasicPass)) == 1
	if !userOK || !passOK {
		w.WriteHeader(http.StatusUnauthorized)
		s.renderer.Render(w, "login", loginPageData{
			NextPath: next,
			Error:    "Wrong username or password.",
		})
		return
	}

	setSessionCookie(w, r, username, s.cfg.BasicPass)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w, r)
	if s.authEnabled() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// authEnabled reports whether BASIC_USER and BASIC_PASS are both set —
// when either is empty, auth middleware is a no-op (local-dev convenience)
// and the login routes are unreachable.
func (s *Server) authEnabled() bool {
	return s.cfg.BasicUser != "" && s.cfg.BasicPass != ""
}

// safeNextPath rejects external URLs / open-redirect attempts. Returns "/"
// when the candidate isn't a relative path inside this app.
func safeNextPath(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || !strings.HasPrefix(candidate, "/") {
		return "/"
	}
	// Reject protocol-relative URLs ("//evil.com/path") and the login page
	// itself (no point redirecting to login after login).
	if strings.HasPrefix(candidate, "//") || strings.HasPrefix(candidate, "/login") {
		return "/"
	}
	return candidate
}
