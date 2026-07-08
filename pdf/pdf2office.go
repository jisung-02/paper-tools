package pdf

import (
	"errors"
	"regexp"
	"strings"
)

// ponytail: text-only reflow, like DocxToHwpx/HwpxToDocx — layout, images,
// tables and fonts are dropped. ExtractText only emits a single "\n" per
// content-stream line break (Td/TD/T*/'/") and "\n\n" between pages, so real
// paragraph boundaries aren't distinguishable from ordinary line wraps within
// a page; blank lines (including page breaks) are treated as paragraph
// breaks and everything between them is joined into one flowing paragraph.

// blankLineRE matches a blank line (a run of whitespace containing at least
// two newlines), used to split extracted text into paragraph blocks.
var blankLineRE = regexp.MustCompile(`\n[ \t\r]*\n`)

// wsRunRE matches any run of whitespace, used to collapse the line breaks
// and indentation inside a single paragraph block down to single spaces.
var wsRunRE = regexp.MustCompile(`\s+`)

// splitTextParagraphs turns raw ExtractText output into a list of paragraphs:
// blank lines (and page breaks) separate paragraphs, while single line
// breaks within a block are collapsed to spaces. Empty blocks are dropped.
func splitTextParagraphs(text string) []string {
	var paras []string
	for _, block := range blankLineRE.Split(text, -1) {
		p := strings.TrimSpace(wsRunRE.ReplaceAllString(block, " "))
		if p != "" {
			paras = append(paras, p)
		}
	}
	return paras
}

// pdfToParas extracts a PDF's text and splits it into paragraphs, the shared
// first step of PdfToDocx and PdfToHwpx.
func pdfToParas(file []byte) ([]string, error) {
	text, err := ExtractText(file)
	if err != nil {
		return nil, err
	}
	paras := splitTextParagraphs(text)
	if len(paras) == 0 {
		return nil, errors.New("PDF에서 추출할 텍스트가 없습니다")
	}
	return paras, nil
}

// PdfToDocx converts a PDF to a .docx file by extracting its text and
// rebuilding it as paragraphs. Layout, images and tables are not preserved.
func PdfToDocx(file []byte) ([]byte, error) {
	paras, err := pdfToParas(file)
	if err != nil {
		return nil, err
	}
	return buildDocx(paras), nil
}

// PdfToHwpx converts a PDF to a .hwpx file by extracting its text and
// rebuilding it as paragraphs. Layout, images and tables are not preserved.
func PdfToHwpx(file []byte) ([]byte, error) {
	paras, err := pdfToParas(file)
	if err != nil {
		return nil, err
	}
	return buildHwpx(paras), nil
}
