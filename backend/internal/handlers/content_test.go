package handlers

import (
	"strings"
	"testing"
)

func TestNormalizeKind(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"", KindHTML, true}, // empty defaults to html (back-compat)
		{"html", KindHTML, true},
		{"markdown", KindMarkdown, true},
		{"txt", KindText, true},
		{"HTML", "", false}, // case-sensitive
		{"md", "", false},   // not the accepted spelling
		{"text", "", false},
		{"pdf", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeKind(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("normalizeKind(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestExtForKind(t *testing.T) {
	cases := map[string]string{
		KindHTML:     "html",
		KindMarkdown: "md",
		KindText:     "txt",
		"anything":   "html", // unknown falls back to html
	}
	for kind, want := range cases {
		if got := extForKind(kind); got != want {
			t.Errorf("extForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestRenderContentHTMLIsVerbatim(t *testing.T) {
	src := `<h1>Hi</h1><script>alert(1)</script>`
	got := string(renderContent(KindHTML, "title", src))
	if got != src {
		t.Errorf("html kind should be served byte-for-byte; got %q", got)
	}
}

func TestRenderContentText(t *testing.T) {
	got := string(renderContent(KindText, "my <title>", "line1\n<b>x</b> & y"))

	// HTML in the body is escaped, not interpreted.
	if strings.Contains(got, "<b>x</b>") {
		t.Errorf("txt body should be escaped; got %q", got)
	}
	if !strings.Contains(got, "&lt;b&gt;x&lt;/b&gt; &amp; y") {
		t.Errorf("txt body missing escaped content; got %q", got)
	}
	// Newlines are preserved inside a <pre> whose CSS wraps long lines.
	if !strings.Contains(got, "line1\n") {
		t.Errorf("txt body should preserve newlines; got %q", got)
	}
	if !strings.Contains(got, `<pre class="content text">`) {
		t.Errorf("txt should render inside the wrapping <pre>; got %q", got)
	}
	// The title is escaped in <title>.
	if !strings.Contains(got, "<title>my &lt;title&gt;</title>") {
		t.Errorf("title not escaped; got %q", got)
	}
}

func TestRenderContentMarkdown(t *testing.T) {
	got := string(renderContent(KindMarkdown, "doc", "# Heading\n\nsome **bold** text\n"))

	if !strings.Contains(got, "<h1") || !strings.Contains(got, "Heading</h1>") {
		t.Errorf("markdown heading not rendered; got %q", got)
	}
	if !strings.Contains(got, "<strong>bold</strong>") {
		t.Errorf("markdown emphasis not rendered; got %q", got)
	}
	if !strings.Contains(got, `<article class="content">`) {
		t.Errorf("markdown should render inside the article shell; got %q", got)
	}
}

func TestRenderContentMarkdownGFMTable(t *testing.T) {
	src := "| a | b |\n| - | - |\n| 1 | 2 |\n"
	got := string(renderContent(KindMarkdown, "doc", src))
	if !strings.Contains(got, "<table>") || !strings.Contains(got, "<td>1</td>") {
		t.Errorf("GFM table not rendered; got %q", got)
	}
}
