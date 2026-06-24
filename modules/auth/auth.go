// Package auth provides a minimal single-password login for the site, using
// only the standard library. The password is never stored in the clear: the
// configured value is the SHA-256 hex digest of the password, and submitted
// passwords are compared by digest in constant time. Logged-in users hold a
// random session token in an HttpOnly cookie; the set of valid tokens lives in
// memory (so sessions are lost on restart, which is fine for a local tool).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
)

// cookieName is the session cookie key.
const cookieName = "session"

// Authenticator guards HTTP handlers behind a single password.
type Authenticator struct {
	passwordHash string // lowercase SHA-256 hex; empty disables auth

	mu       sync.Mutex
	sessions map[string]bool
}

// New returns an Authenticator. passwordHash is the SHA-256 hex digest of the
// password (see HashPassword). When it is empty, auth is disabled and every
// request passes through.
func New(passwordHash string) *Authenticator {
	return &Authenticator{
		passwordHash: strings.ToLower(strings.TrimSpace(passwordHash)),
		sessions:     make(map[string]bool),
	}
}

// HashPassword returns the SHA-256 hex digest of a password, the form expected
// by New (and stored in AUTH_PASSWORD_HASH).
func HashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

// Enabled reports whether a password is configured.
func (a *Authenticator) Enabled() bool {
	return a.passwordHash != ""
}

// checkPassword reports whether password matches the configured hash, comparing
// in constant time.
func (a *Authenticator) checkPassword(password string) bool {
	want := []byte(a.passwordHash)
	got := []byte(HashPassword(password))
	return subtle.ConstantTimeCompare(want, got) == 1
}

// newSession creates and records a session token.
func (a *Authenticator) newSession() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	a.mu.Lock()
	a.sessions[token] = true
	a.mu.Unlock()
	return token, nil
}

// valid reports whether token is a live session.
func (a *Authenticator) valid(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[token]
}

// destroy invalidates a session token.
func (a *Authenticator) destroy(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// authenticated reports whether the request carries a valid session cookie.
func (a *Authenticator) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return a.valid(cookie.Value)
}

// Register wires the login and logout routes onto mux. It is a no-op when auth
// is disabled.
func (a *Authenticator) Register(mux *http.ServeMux) {
	if !a.Enabled() {
		return
	}
	mux.HandleFunc("/login", a.login)
	mux.HandleFunc("/logout", a.logout)
}

// Middleware wraps next, requiring a valid session for every request except the
// login and logout routes. Unauthenticated API requests get 401; unauthenticated
// page requests are redirected to the login form. When auth is disabled it
// returns next unchanged.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	if !a.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || r.URL.Path == "/logout" || a.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "Authentication required", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

// login serves the login form (GET) and checks the submitted password (POST),
// setting a session cookie on success.
func (a *Authenticator) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if a.authenticated(r) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		writeLoginForm(w, "")
	case http.MethodPost:
		if !a.checkPassword(r.FormValue("password")) {
			w.WriteHeader(http.StatusUnauthorized)
			writeLoginForm(w, "Wrong password.")
			return
		}
		token, err := a.newSession()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// logout clears the session and its cookie, then redirects to the login form.
func (a *Authenticator) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		a.destroy(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// writeLoginForm renders the login page, optionally showing an error message.
func writeLoginForm(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>Sign in &mdash; CiteScout</title></head>
<body>
<h1>Sign in</h1>`))
	if errMsg != "" {
		w.Write([]byte("<p style=\"color:red\">" + errMsg + "</p>"))
	}
	w.Write([]byte(`<form action="/login" method="post">
	<p><label>Password<br><input type="password" name="password" autofocus></label></p>
	<p><button type="submit">Sign in</button></p>
</form>
</body>
</html>`))
}
