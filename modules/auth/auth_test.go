package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// okHandler is a trivial protected handler.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("secret"))
})

func TestDisabledAuthPassesThrough(t *testing.T) {
	a := New("") // no password configured
	if a.Enabled() {
		t.Fatal("Enabled() = true with empty hash")
	}
	rec := httptest.NewRecorder()
	a.Middleware(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/papers/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (auth disabled)", rec.Code)
	}
}

func TestMiddlewareBlocksUnauthenticated(t *testing.T) {
	a := New(HashPassword("hunter2"))

	// A page request is redirected to the login form.
	rec := httptest.NewRecorder()
	a.Middleware(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/papers/", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("page request: status %d loc %q, want 303 -> /login", rec.Code, rec.Header().Get("Location"))
	}

	// An API request gets 401, not a redirect.
	rec = httptest.NewRecorder()
	a.Middleware(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/papers/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("api request status = %d, want 401", rec.Code)
	}
}

func TestLoginFlowGrantsAccess(t *testing.T) {
	a := New(HashPassword("hunter2"))
	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/papers/", okHandler)
	srv := a.Middleware(mux)

	// Wrong password is rejected.
	rec := httptest.NewRecorder()
	postLogin(srv, rec, "wrong")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", rec.Code)
	}

	// Correct password sets a session cookie.
	rec = httptest.NewRecorder()
	postLogin(srv, rec, "hunter2")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", rec.Code)
	}
	cookie := sessionCookie(rec)
	if cookie == nil {
		t.Fatal("login did not set a session cookie")
	}

	// The cookie unlocks a protected route.
	req := httptest.NewRequest(http.MethodGet, "/papers/", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated request status = %d, want 200", rec.Code)
	}

	// After logout the cookie no longer works.
	req = httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodGet, "/papers/", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Error("session still valid after logout")
	}
}

func postLogin(h http.Handler, rec *httptest.ResponseRecorder, password string) {
	form := url.Values{"password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value != "" {
			return c
		}
	}
	return nil
}
