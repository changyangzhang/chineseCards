package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sessionCookieName is the cookie we set on successful login. Path=/ so it
// covers every authenticated route.
const sessionCookieName = "cc_session"

// sessionTTL is how long a login lasts. Long enough to be ergonomic for a
// personal app, short enough that a forgotten browser doesn't stay logged
// in indefinitely.
const sessionTTL = 7 * 24 * time.Hour

// sessionToken is what we put in the cookie:
//
//	base64url(username) "." expiryUnix "." base64url(hmacSHA256(payload, key))
//
// The HMAC key is derived from the configured password — when the password
// changes, all existing sessions invalidate automatically. No new env var,
// no server-side session store.
//
// Single-user app: we don't need anti-replay, refresh tokens, or rotation.
// Keep it small enough that a misconfigured browser can't trip over it.

// newSessionToken builds a signed token for username, expiring at exp.
func newSessionToken(username string, exp time.Time, key []byte) string {
	expStr := strconv.FormatInt(exp.Unix(), 10)
	payload := base64.RawURLEncoding.EncodeToString([]byte(username)) + "." + expStr
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

// parseSessionToken verifies the signature and expiry, returning the username
// on success. Constant-time signature compare to prevent timing oracles.
func parseSessionToken(token string, key []byte) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("session: malformed token")
	}
	payload := parts[0] + "." + parts[1]

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("session: bad signature encoding")
	}
	if subtle.ConstantTimeCompare(expected, got) != 1 {
		return "", errors.New("session: bad signature")
	}

	expSecs, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", errors.New("session: bad expiry")
	}
	if time.Now().Unix() >= expSecs {
		return "", errors.New("session: expired")
	}

	userBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("session: bad username encoding")
	}
	return string(userBytes), nil
}

// sessionKey derives the HMAC key from the configured password. Tying the
// key to the password means password changes invalidate all sessions for
// free.
func sessionKey(password string) []byte {
	h := sha256.Sum256([]byte("chinesecards.session." + password))
	return h[:]
}

// setSessionCookie writes a fresh session cookie on the response.
func setSessionCookie(w http.ResponseWriter, r *http.Request, username, password string) {
	exp := time.Now().Add(sessionTTL)
	token := newSessionToken(username, exp, sessionKey(password))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil, // Secure on HTTPS deployments (Fly), open on local
	})
}

// clearSessionCookie sets an immediately-expired cookie of the same name.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
}

// readSessionCookie returns the username if the request carries a valid
// signed session cookie, or "" otherwise.
func readSessionCookie(r *http.Request, password string) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	username, err := parseSessionToken(c.Value, sessionKey(password))
	if err != nil {
		return ""
	}
	return username
}
