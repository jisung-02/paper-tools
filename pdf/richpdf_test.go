package pdf

import (
	"bytes"
	"strings"
	"testing"
)

func TestWrapSpansStyledBreak(t *testing.T) {
	font := testFont(t)
	f, err := parseTTF(font)
	if err != nil {
		t.Fatalf("parseTTF: %v", err)
	}
	spans := []pdfSpan{
		{text: "bold words here", size: 12, bold: true},
		{text: " and plain tail that wraps", size: 12},
	}
	lines := wrapSpans(f, spans, 80) // narrow → forces wrapping
	if len(lines) < 2 {
		t.Fatalf("expected wrapping, got %d line(s)", len(lines))
	}
	var joined []string
	for _, ln := range lines {
		var sb strings.Builder
		for _, sp := range ln {
			sb.WriteString(sp.text)
		}
		joined = append(joined, sb.String())
	}
	all := strings.Join(joined, " ")
	if !strings.Contains(all, "bold words") || !strings.Contains(all, "wraps") {
		t.Errorf("text lost in wrap: %q", joined)
	}
	if !lines[0][0].bold {
		t.Errorf("first line lost bold styling: %+v", lines[0])
	}
}

func TestRenderDocPDFFormatting(t *testing.T) {
	font := testFont(t)
	doc := &DocModel{Blocks: []Block{
		&Para{Heading: 1, Runs: []Run{{Text: "제목입니다"}}},
		&Para{Align: AlignCenter, Runs: []Run{
			{Text: "굵은글씨 ", Bold: true},
			{Text: "기울임 ", Italic: true},
			{Text: "빨강밑줄", Color: 0xFF0000, Underline: true, Strike: true},
		}},
		&Para{Runs: []Run{{Text: "본문 문단입니다"}}},
	}}
	pdfBytes, err := renderDocPDF(doc, font, TextPDFOpts{})
	if err != nil {
		t.Fatalf("renderDocPDF: %v", err)
	}
	if !strings.HasPrefix(string(pdfBytes), "%PDF") {
		t.Fatal("not a PDF")
	}
	if _, err := Parse(pdfBytes); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	text, err := ExtractText(pdfBytes)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	for _, want := range []string{"제목입니다", "굵은글씨", "본문 문단입니다"} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q; got %q", want, text)
		}
	}
	// Content streams are uncompressed: assert the synthetic-style operators.
	if !bytes.Contains(pdfBytes, []byte("2 Tr")) {
		t.Error("missing synthetic-bold render mode (2 Tr)")
	}
	if !bytes.Contains(pdfBytes, []byte("1 0 0.21 1")) {
		t.Error("missing italic shear matrix")
	}
	if !bytes.Contains(pdfBytes, []byte("1.000 0.000 0.000 rg")) {
		t.Error("missing red fill color")
	}
}

func TestRenderDocPDFEmptyDoc(t *testing.T) {
	pdfBytes, err := renderDocPDF(&DocModel{}, testFont(t), TextPDFOpts{})
	if err != nil {
		t.Fatalf("renderDocPDF empty: %v", err)
	}
	if _, err := Parse(pdfBytes); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestRenderDocPDFHeadingSizePinned(t *testing.T) {
	doc := &DocModel{Blocks: []Block{
		&Para{Heading: 1, Runs: []Run{{Text: "제목"}}},
	}}
	pdfBytes, err := renderDocPDF(doc, testFont(t), TextPDFOpts{FontSize: 20})
	if err != nil {
		t.Fatalf("renderDocPDF: %v", err)
	}
	// Heading size is pinned to headingSizePt(1)=22, not 20*2.0=40.
	if !bytes.Contains(pdfBytes, []byte("/F1 22.00 Tf")) {
		t.Error("heading not rendered at pinned 22pt")
	}
	if bytes.Contains(pdfBytes, []byte("/F1 40.00 Tf")) {
		t.Error("heading scaled with body FontSize; must stay pinned")
	}
}

func TestRenderDocPDFTable(t *testing.T) {
	doc := &DocModel{Blocks: []Block{
		&Para{Runs: []Run{{Text: "표 앞 문단"}}},
		&Table{Rows: [][]Cell{
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "머리칸", Bold: true}}}}, ColSpan: 2},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "세로칸"}}}}, RowSpan: 2}},
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "한"}}}}},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "둘"}}}}}},
		}},
		&Para{Runs: []Run{{Text: "표 뒤 문단"}}},
	}}
	pdfBytes, err := renderDocPDF(doc, testFont(t), TextPDFOpts{})
	if err != nil {
		t.Fatalf("renderDocPDF: %v", err)
	}
	if _, err := Parse(pdfBytes); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	text, err := ExtractText(pdfBytes)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	for _, want := range []string{"표 앞 문단", "머리칸", "세로칸", "한", "둘", "표 뒤 문단"} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q", want)
		}
	}
	if !bytes.Contains(pdfBytes, []byte("re S")) {
		t.Error("no grid rectangles stroked")
	}
}

func TestRenderDocPDFTableSplitsAcrossPages(t *testing.T) {
	tbl := &Table{}
	for i := 0; i < 80; i++ {
		tbl.Rows = append(tbl.Rows, []Cell{
			{Blocks: []Block{&Para{Runs: []Run{{Text: "왼쪽 칸 내용"}}}}},
			{Blocks: []Block{&Para{Runs: []Run{{Text: "오른쪽 칸 내용"}}}}},
		})
	}
	pdfBytes, err := renderDocPDF(&DocModel{Blocks: []Block{tbl}}, testFont(t), TextPDFOpts{})
	if err != nil {
		t.Fatalf("renderDocPDF: %v", err)
	}
	if _, err := Parse(pdfBytes); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// pdf/ops.go's writeObj sorts Dict keys alphabetically, so a real Page
	// object's "/Type" key (alphabetically last among Contents/MediaBox/
	// Parent/Resources/Type) is always immediately followed by " >>". The
	// single Pages object's "/Type /Pages" would also match a bare
	// "/Type /Page" substring search (it's a byte-for-byte prefix), silently
	// inflating the count by one and masking a single-page regression. The
	// trailing space distinguishes them: "/Type /Page " never matches
	// "/Type /Pages ..." since the next byte there is 's', not a space.
	if n := bytes.Count(pdfBytes, []byte("/Type /Page ")); n < 2 {
		t.Errorf("80-row table should span 2+ pages, got %d page objects", n)
	}
}
