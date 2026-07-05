package pdf

import (
	"os"
	"strings"
	"testing"
)

func testFont(t *testing.T) []byte {
	t.Helper()
	font, err := os.ReadFile("../web/NanumGothic-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	return font
}

func TestMarkdownToPDFBasic(t *testing.T) {
	font := testFont(t)
	md := "# Title\n\nHello world paragraph.\n"
	out, err := MarkdownToPDF([]byte(md), font, MarkdownPDFOpts{})
	if err != nil {
		t.Fatalf("MarkdownToPDF: %v", err)
	}
	if !strings.HasPrefix(string(out), "%PDF") {
		t.Fatalf("output does not start with %%PDF")
	}
	if _, err := Parse(out); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	for _, want := range []string{"Title", "Hello world paragraph."} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q; got:\n%s", want, text)
		}
	}
}

func TestMarkdownToPDFHeadingSizes(t *testing.T) {
	font := testFont(t)
	f, err := parseTTF(font)
	if err != nil {
		t.Fatalf("parseTTF: %v", err)
	}
	md := "# H1\n\n## H2\n\n### H3\n\nBody text.\n"
	blocks := parseMarkdown(md)
	lines := layoutMarkdown(f, blocks, 11)

	var h1, h2, h3, body float64
	for _, ln := range lines {
		switch ln.text {
		case "H1":
			h1 = ln.fontSize
		case "H2":
			h2 = ln.fontSize
		case "H3":
			h3 = ln.fontSize
		case "Body text.":
			body = ln.fontSize
		}
	}
	if h1 <= h2 || h2 <= h3 || h3 <= body {
		t.Fatalf("expected h1 > h2 > h3 > body, got h1=%v h2=%v h3=%v body=%v", h1, h2, h3, body)
	}
	if body != 11 {
		t.Fatalf("expected body font size 11, got %v", body)
	}

	// Render it too, to make sure varying font sizes per line doesn't break
	// PDF generation.
	out, err := MarkdownToPDF([]byte(md), font, MarkdownPDFOpts{})
	if err != nil {
		t.Fatalf("MarkdownToPDF: %v", err)
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	for _, want := range []string{"H1", "H2", "H3", "Body text."} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q", want)
		}
	}
}

func TestMarkdownToPDFListIndent(t *testing.T) {
	font := testFont(t)
	f, err := parseTTF(font)
	if err != nil {
		t.Fatalf("parseTTF: %v", err)
	}
	md := "- top level item\n  - nested item\n1. first\n2. second\n"
	blocks := parseMarkdown(md)
	lines := layoutMarkdown(f, blocks, 11)

	var topIndent, nestedIndent = -1.0, -1.0
	var sawOrdered1, sawOrdered2 bool
	for _, ln := range lines {
		switch {
		case strings.Contains(ln.text, "top level item"):
			topIndent = ln.indent
		case strings.Contains(ln.text, "nested item"):
			nestedIndent = ln.indent
		case strings.HasPrefix(ln.text, "1. "):
			sawOrdered1 = true
		case strings.HasPrefix(ln.text, "2. "):
			sawOrdered2 = true
		}
	}
	if topIndent < 0 || nestedIndent < 0 {
		t.Fatalf("did not find both list lines: top=%v nested=%v", topIndent, nestedIndent)
	}
	if nestedIndent <= topIndent {
		t.Fatalf("expected nested indent (%v) > top indent (%v)", nestedIndent, topIndent)
	}
	if !sawOrdered1 || !sawOrdered2 {
		t.Fatalf("expected ordered list items to keep their numbers: got 1.=%v 2.=%v", sawOrdered1, sawOrdered2)
	}
}

func TestMarkdownToPDFCodeBlockVerbatim(t *testing.T) {
	font := testFont(t)
	md := "Some text.\n\n```\nfunc main() {\n    x :=   1  +  2\n}\n```\n\nAfter.\n"
	out, err := MarkdownToPDF([]byte(md), font, MarkdownPDFOpts{})
	if err != nil {
		t.Fatalf("MarkdownToPDF: %v", err)
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	// The code block's internal spacing must survive untouched (no reflow,
	// no whitespace collapsing), unlike a reflowed paragraph.
	if !strings.Contains(text, "x :=   1  +  2") {
		t.Errorf("code block was not preserved verbatim; got:\n%s", text)
	}
	if !strings.Contains(text, "func main() {") {
		t.Errorf("code block missing content; got:\n%s", text)
	}
}

func TestMarkdownToPDFIndentedCodeBlock(t *testing.T) {
	font := testFont(t)
	md := "Paragraph.\n\n    indented code line\n    second line\n\nAfter.\n"
	out, err := MarkdownToPDF([]byte(md), font, MarkdownPDFOpts{})
	if err != nil {
		t.Fatalf("MarkdownToPDF: %v", err)
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(text, "indented code line") || !strings.Contains(text, "second line") {
		t.Errorf("indented code block missing content; got:\n%s", text)
	}
}

func TestMarkdownToPDFKorean(t *testing.T) {
	font := testFont(t)
	md := "# 제목입니다\n\n안녕하세요 Hello World\n둘째 줄 123\n\n- 목록 항목\n"
	out, err := MarkdownToPDF([]byte(md), font, MarkdownPDFOpts{})
	if err != nil {
		t.Fatalf("MarkdownToPDF: %v", err)
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	for _, want := range []string{"제목입니다", "안녕하세요", "Hello World", "둘째", "목록 항목"} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q; got:\n%s", want, text)
		}
	}
}

func TestMarkdownToPDFInlineAndStructure(t *testing.T) {
	font := testFont(t)
	md := strings.Join([]string{
		"# Heading",
		"",
		"A paragraph with **bold**, *italic*, and `code` plus a [link](https://example.com) and an image ![alt](https://example.com/x.png) that should vanish.",
		"",
		"> a quoted line",
		"",
		"---",
		"",
		"- one",
		"- two",
	}, "\n")

	out, err := MarkdownToPDF([]byte(md), font, MarkdownPDFOpts{})
	if err != nil {
		t.Fatalf("MarkdownToPDF: %v", err)
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	for _, want := range []string{"bold", "italic", "code", "link", "a quoted line", "one", "two"} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q; got:\n%s", want, text)
		}
	}
	for _, notWant := range []string{"**bold**", "*italic*", "`code`", "https://example.com", "alt", "![", "]("} {
		if strings.Contains(text, notWant) {
			t.Errorf("extracted text should not contain markdown syntax %q; got:\n%s", notWant, text)
		}
	}
}

func TestMarkdownToPDFEmptyInput(t *testing.T) {
	font := testFont(t)
	if _, err := MarkdownToPDF([]byte(""), font, MarkdownPDFOpts{}); err == nil {
		t.Fatal("expected error for empty markdown input")
	}
	if _, err := MarkdownToPDF([]byte("   \n\t\n  "), font, MarkdownPDFOpts{}); err == nil {
		t.Fatal("expected error for whitespace-only markdown input")
	}
}

func TestMarkdownToPDFInvalidFont(t *testing.T) {
	if _, err := MarkdownToPDF([]byte("# hi"), []byte("not a font"), MarkdownPDFOpts{}); err == nil {
		t.Fatal("expected error for invalid font bytes")
	}
}
