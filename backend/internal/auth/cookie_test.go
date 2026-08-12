package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSetSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	expires := time.Now().Add(SessionTTL)
	SetSessionCookie(rec, "token-abc", expires, true)

	c := findCookie(rec.Result().Cookies(), SessionCookieName)
	if c == nil {
		t.Fatalf("expected a %q cookie to be set", SessionCookieName)
	}
	if c.Value != "token-abc" {
		t.Errorf("cookie value = %q, want %q", c.Value, "token-abc")
	}
	if c.Path != "/" {
		t.Errorf("cookie path = %q, want %q", c.Path, "/")
	}
	if !c.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if !c.Secure {
		t.Error("cookie should be Secure when the request was")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.MaxAge <= 0 {
		t.Errorf("cookie MaxAge = %d, want a positive value", c.MaxAge)
	}
}

// The unset direction is the whole point of the parameter: a browser discards a
// Secure cookie that arrives over plain HTTP from anything but localhost, which
// made self-hosting over http://<lan-ip> present as a login that never took.
// Everything except Secure must stay identical.
func TestSetSessionCookieInsecureRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "token-abc", time.Now().Add(SessionTTL), false)

	c := findCookie(rec.Result().Cookies(), SessionCookieName)
	if c == nil {
		t.Fatalf("expected a %q cookie to be set", SessionCookieName)
	}
	if c.Secure {
		t.Error("cookie must not be Secure over a plain-HTTP request, or the browser drops it")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("only Secure may differ over plain HTTP, got %+v", c)
	}
}

func TestClearSessionCookie(t *testing.T) {
	for _, secure := range []bool{true, false} {
		rec := httptest.NewRecorder()
		ClearSessionCookie(rec, secure)

		c := findCookie(rec.Result().Cookies(), SessionCookieName)
		if c == nil {
			t.Fatalf("expected a %q cookie to be set", SessionCookieName)
		}
		if c.Value != "" {
			t.Errorf("cleared cookie value = %q, want empty", c.Value)
		}
		if c.MaxAge != -1 {
			t.Errorf("cleared cookie MaxAge = %d, want -1", c.MaxAge)
		}
		// Mirrors SetSessionCookie so logout deletes the same cookie it set.
		if c.Secure != secure {
			t.Errorf("cleared cookie Secure = %v, want %v", c.Secure, secure)
		}
	}
}
