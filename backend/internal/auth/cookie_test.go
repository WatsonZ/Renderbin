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
	SetSessionCookie(rec, "token-abc", expires)

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
		t.Error("cookie should be Secure")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.MaxAge <= 0 {
		t.Errorf("cookie MaxAge = %d, want a positive value", c.MaxAge)
	}
}

func TestClearSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec)

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
}
