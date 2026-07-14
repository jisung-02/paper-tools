package pdf

import (
	"strings"
	"testing"
)

func TestMarkdownToHTMLBlocks(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want []string // substrings that must all appear, in the output
	}{
		{"heading h1", "# Title", []string{"<h1>Title</h1>"}},
		{"heading h3", "### Sub", []string{"<h3>Sub</h3>"}},
		{"heading h6 clamps from more hashes not possible", "###### Deep", []string{"<h6>Deep</h6>"}},
		{"paragraph", "Hello world.", []string{"<p>Hello world.</p>"}},
		{"blockquote", "> quoted text", []string{"<blockquote><p>quoted text</p></blockquote>"}},
		{"hr dashes", "---", []string{"<hr>"}},
		{"hr stars spaced", "* * *", []string{"<hr>"}},
		{"fenced code", "```\nline1\nline2\n```", []string{"<pre><code>line1\nline2</code></pre>"}},
		{"indented code", "    code here", []string{"<pre><code>code here</code></pre>"}},
		{"unordered list", "- a\n- b", []string{"<ul><li>a</li><li>b</li></ul>"}},
		{"ordered list", "1. a\n2. b", []string{"<ol><li>a</li><li>b</li></ol>"}},
		{"nested list", "- a\n  - b\n- c", []string{"<ul><li>a<ul><li>b</li></ul></li><li>c</li></ul>"}},
		{"ordered list starting past 1", "5. five\n6. six", []string{`<ol start="5"><li>five</li><li>six</li></ol>`}},
		{"level-1-first run has no phantom bullet", "  - nested\n- top", []string{"<ul><li>nested</li><li>top</li></ul>"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(MarkdownToHTML([]byte(tc.md)))
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("MarkdownToHTML(%q) = %q; want substring %q", tc.md, got, want)
				}
			}
		})
	}
}

func TestMarkdownToHTMLInline(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want string
	}{
		{"bold star", "**bold**", "<p><strong>bold</strong></p>"},
		{"bold underscore", "__bold__", "<p><strong>bold</strong></p>"},
		{"italic star", "*italic*", "<p><em>italic</em></p>"},
		{"italic underscore", "_italic_", "<p><em>italic</em></p>"},
		{"code span", "`code`", "<p><code>code</code></p>"},
		{"nested bold italic", "**_both_**", "<p><strong><em>both</em></strong></p>"},
		{"image dropped", "before ![alt](http://x.com/a.png) after", "<p>before  after</p>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(MarkdownToHTML([]byte(tc.md)))
			if got != tc.want {
				t.Errorf("MarkdownToHTML(%q) = %q; want %q", tc.md, got, tc.want)
			}
		})
	}
}

func TestMarkdownToHTMLLinks(t *testing.T) {
	cases := []struct {
		name       string
		md         string
		wantHas    []string
		wantAbsent []string
	}{
		{
			"https link gets target/rel",
			"[go](https://example.com)",
			[]string{`<a href="https://example.com" target="_blank" rel="noopener">go</a>`},
			nil,
		},
		{
			"http link gets target/rel",
			"[go](http://example.com)",
			[]string{`<a href="http://example.com" target="_blank" rel="noopener">go</a>`},
			nil,
		},
		{
			"mailto link has no target/rel",
			"[mail](mailto:a@b.com)",
			[]string{`<a href="mailto:a@b.com">mail</a>`},
			[]string{"target=", "rel="},
		},
		{
			"fragment link has no target/rel",
			"[jump](#section)",
			[]string{`<a href="#section">jump</a>`},
			[]string{"target=", "rel="},
		},
		{
			"relative path link has no target/rel",
			"[page](docs/page.html)",
			[]string{`<a href="docs/page.html">page</a>`},
			[]string{"target=", "rel="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(MarkdownToHTML([]byte(tc.md)))
			for _, want := range tc.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("MarkdownToHTML(%q) = %q; want substring %q", tc.md, got, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("MarkdownToHTML(%q) = %q; must not contain %q", tc.md, got, absent)
				}
			}
		})
	}
}

// TestMarkdownToHTMLXSS is the non-negotiable security contract: the output
// is injected into the DOM via innerHTML, so none of these inputs may ever
// produce live markup, an event handler attribute, a script tag, or a
// javascript:/data:/vbscript: URI in an href.
func TestMarkdownToHTMLXSS(t *testing.T) {
	// Only unescaped tag openings and dangerous URI schemes are actual
	// live-markup risk; "onerror=" etc. appearing inside already-escaped
	// text (e.g. "&lt;img src=x onerror=...&gt;") is inert and fine.
	dangerous := []string{
		"<script>", "<img", "<svg", "<iframe", "javascript:", "vbscript:",
	}
	assertSafe := func(t *testing.T, got string) {
		t.Helper()
		lower := strings.ToLower(got)
		for _, bad := range dangerous {
			if strings.Contains(lower, strings.ToLower(bad)) {
				t.Errorf("output contains dangerous fragment %q; full output: %q", bad, got)
			}
		}
	}

	cases := []struct {
		name string
		md   string
	}{
		{"script tag as text", "<script>alert(1)</script>"},
		{"script tag in paragraph", "hello <script>alert(1)</script> world"},
		{"img onerror as text", `<img src=x onerror=alert(1)>`},
		{"javascript href lowercase", "[x](javascript:alert(1))"},
		{"javascript href mixed case", "[x](JaVaScRiPt:alert(1))"},
		{"javascript href leading space", "[x]( javascript:alert(1))"},
		{"javascript href tab trick", "[x](java\tscript:alert(1))"},
		{"data href", "[x](data:text/html,<script>alert(1)</script>)"},
		{"vbscript href", "[x](vbscript:msgbox(1))"},
		{"quote injection in link text", `[a"onmouseover="alert(1)](https://example.com)`},
		{"quote injection in href", `[x](https://example.com/"onmouseover="alert(1))`},
		{"raw html in heading", "# <script>alert(1)</script>"},
		{"raw html in list item", "- <img src=x onerror=alert(1)>"},
		{"raw html in blockquote", "> <script>alert(1)</script>"},
		{"raw html in code span", "`<script>alert(1)</script>`"},
		{"raw html in bold", "**<script>alert(1)</script>**"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(MarkdownToHTML([]byte(tc.md)))
			assertSafe(t, got)
		})
	}

	// Escaping must actually happen (not just "no dangerous substring" by
	// accident of the text being dropped) - the escaped angle brackets must
	// be present as literal escaped text.
	t.Run("script tag is escaped not dropped", func(t *testing.T) {
		got := string(MarkdownToHTML([]byte("<script>alert(1)</script>")))
		if !strings.Contains(got, "&lt;script&gt;") {
			t.Errorf("expected escaped script tag, got %q", got)
		}
	})

	t.Run("javascript href renders label as plain text not a link", func(t *testing.T) {
		got := string(MarkdownToHTML([]byte("[x](javascript:alert(1))")))
		if strings.Contains(got, "<a ") {
			t.Errorf("expected no <a> tag, got %q", got)
		}
		if !strings.Contains(got, "x") {
			t.Errorf("expected label text to survive as plain text, got %q", got)
		}
	})

	t.Run("quotes in href are escaped so attribute cannot be broken out of", func(t *testing.T) {
		got := string(MarkdownToHTML([]byte(`[x](https://example.com/"onmouseover="alert(1))`)))
		if strings.Contains(got, `onmouseover="alert`) {
			t.Errorf("raw quote broke out of href attribute: %q", got)
		}
	})
}

func TestMarkdownToHTMLEmptyInput(t *testing.T) {
	got := MarkdownToHTML([]byte(""))
	if len(got) != 0 {
		t.Errorf("MarkdownToHTML(\"\") = %q; want empty", got)
	}
}
