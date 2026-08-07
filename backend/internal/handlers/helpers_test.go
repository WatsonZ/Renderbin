package handlers

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
)

func TestNormalizeTags(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"a", "a"},
		{" a , b ,c ", "a,b,c"},
		{"a,a,a", "a"},
		{"a,,b", "a,b"},
		{"  ,  ", ""},
		{"b,a,b,c,a", "b,a,c"}, // dedupe preserves first-seen order
	}
	for _, c := range cases {
		if got := normalizeTags(c.in); got != c.want {
			t.Errorf("normalizeTags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAccessCodeMatches(t *testing.T) {
	cases := []struct {
		want, got string
		match     bool
	}{
		{"abc", "abc", true},
		{"", "", true},
		{"abc", "abd", false},
		{"abc", "abcd", false},
		{"abcd", "abc", false},
		{"abc", "", false},
	}
	for _, c := range cases {
		if got := accessCodeMatches(c.want, c.got); got != c.match {
			t.Errorf("accessCodeMatches(%q, %q) = %v, want %v", c.want, c.got, got, c.match)
		}
	}
}

func TestSanitizeASCII(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain.html", "plain.html"},
		{"a b", "a b"},         // space kept
		{"a\"b", "a_b"},        // quote replaced
		{"a\\b", "a_b"},        // backslash replaced
		{"a\tb", "a_b"},        // control char replaced
		{"a\r\nb", "a__b"},     // CR/LF replaced (header-injection guard)
		{"héllo", "h_llo"},     // non-ASCII replaced
		{"测试.html", "__.html"}, // two runes + dot + html
	}
	for _, c := range cases {
		if got := sanitizeASCII(c.in); got != c.want {
			t.Errorf("sanitizeASCII(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDownloadDisposition(t *testing.T) {
	// ASCII name: both the ascii fallback and the RFC 5987 form carry it.
	got := downloadDisposition("report", "slug123", "html")
	if !strings.Contains(got, `filename="report.html"`) {
		t.Errorf("disposition %q missing ascii filename", got)
	}
	if !strings.HasPrefix(got, "attachment; ") {
		t.Errorf("disposition %q should start with attachment;", got)
	}

	// Unicode name: ascii fallback is sanitized, filename* is percent-encoded.
	got = downloadDisposition("测试", "slug123", "html")
	if !strings.Contains(got, `filename="__.html"`) {
		t.Errorf("disposition %q missing sanitized ascii fallback", got)
	}
	if !strings.Contains(got, "filename*=UTF-8''"+url.PathEscape("测试.html")) {
		t.Errorf("disposition %q missing RFC5987 filename*", got)
	}

	// Empty name falls back to slug for both forms.
	got = downloadDisposition("   ", "myslug", "html")
	if !strings.Contains(got, `filename="myslug.html"`) {
		t.Errorf("disposition %q should fall back to slug", got)
	}

	// The extension tracks the file's kind (e.g. markdown → .md).
	got = downloadDisposition("notes", "slug123", "md")
	if !strings.Contains(got, `filename="notes.md"`) {
		t.Errorf("disposition %q should use the .md extension", got)
	}
}

func TestSlugPattern(t *testing.T) {
	valid := []string{"a", "abc-123_.X", strings.Repeat("a", 128)}
	for _, s := range valid {
		if !slugPattern.MatchString(s) {
			t.Errorf("slugPattern rejected valid slug %q", s)
		}
	}
	invalid := []string{"", "a b", "a/b", "a!b", "日本", strings.Repeat("a", 129)}
	for _, s := range invalid {
		if slugPattern.MatchString(s) {
			t.Errorf("slugPattern accepted invalid slug %q", s)
		}
	}
}

func TestContentSnippet(t *testing.T) {
	// No match → empty.
	if got := contentSnippet("hello world", "zzz"); got != "" {
		t.Errorf("contentSnippet(no match) = %q, want empty", got)
	}

	// Short content: whole thing, no ellipses.
	if got := contentSnippet("hello world", "WORLD"); got != "hello world" {
		t.Errorf("contentSnippet(short) = %q", got)
	}

	// Long content: ±100 runes of context with ellipses on both sides.
	long := strings.Repeat("a", 500) + "NEEDLE" + strings.Repeat("b", 500)
	got := contentSnippet(long, "needle")
	want := "…" + strings.Repeat("a", 100) + "NEEDLE" + strings.Repeat("b", 100) + "…"
	if got != want {
		t.Errorf("contentSnippet(long) = %q (len %d), want %q", got, len(got), want)
	}

	// Multi-byte runes: context is counted in runes and stays valid UTF-8.
	cn := strings.Repeat("汉", 300) + "目标" + strings.Repeat("字", 300)
	got = contentSnippet(cn, "目标")
	want = "…" + strings.Repeat("汉", 100) + "目标" + strings.Repeat("字", 100) + "…"
	if got != want {
		t.Errorf("contentSnippet(cjk) = %q", got)
	}

	// Match at the very start: no leading ellipsis.
	got = contentSnippet("NEEDLE"+strings.Repeat("x", 500), "needle")
	if !strings.HasPrefix(got, "NEEDLE") || !strings.HasSuffix(got, "…") {
		t.Errorf("contentSnippet(start) = %q", got)
	}
}

func TestToFileResponse(t *testing.T) {
	created := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)

	// Unset limit columns → nil pointers in the response.
	unset := toFileResponse(sqlcgen.File{
		Slug:      "s",
		Name:      "n",
		CreatedAt: created,
		UpdatedAt: created,
	})
	if unset.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil", *unset.ExpiresAt)
	}
	if unset.MaxViews != nil {
		t.Errorf("MaxViews = %v, want nil", *unset.MaxViews)
	}
	if unset.CreatedAt != "2026-07-16T10:30:00Z" {
		t.Errorf("CreatedAt = %q, want RFC3339 UTC", unset.CreatedAt)
	}

	// Set limit columns → populated pointers.
	set := toFileResponse(sqlcgen.File{
		Slug:      "s",
		CreatedAt: created,
		UpdatedAt: created,
		ExpiresAt: sql.NullTime{Time: created, Valid: true},
		MaxViews:  sql.NullInt64{Int64: 7, Valid: true},
		ViewCount: 3,
	})
	if set.ExpiresAt == nil || *set.ExpiresAt != "2026-07-16T10:30:00Z" {
		t.Errorf("ExpiresAt = %v, want the formatted time", set.ExpiresAt)
	}
	if set.MaxViews == nil || *set.MaxViews != 7 {
		t.Errorf("MaxViews = %v, want 7", set.MaxViews)
	}
	if set.ViewCount != 3 {
		t.Errorf("ViewCount = %d, want 3", set.ViewCount)
	}
}
