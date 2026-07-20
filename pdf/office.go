package pdf

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// ponytail: docx↔hwpx conversion preserves character formatting
// (bold/italic/underline/strike/size/color), alignment and heading levels
// via the shared DocModel; tables and images are later stages.

// splitParas splits extracted text on newlines, trimming ONE trailing empty paragraph
// if the text ends with "\n" (which split produces a trailing "" element).
// Keeps any other internal empty paragraphs (blank lines) as-is.
// Returns at least one paragraph (empty string if input is empty).
func splitParas(text string) []string {
	paras := strings.Split(text, "\n")
	// If the split has at least one element and the last is empty (text ended with "\n"),
	// drop that one trailing empty element only.
	if len(paras) > 0 && paras[len(paras)-1] == "" {
		paras = paras[:len(paras)-1]
	}
	// Ensure we always return at least one paragraph.
	if len(paras) == 0 {
		paras = []string{""}
	}
	return paras
}

// escapeXMLText safely escapes a string for use in XML text content.
// xml.EscapeText only escapes & < > ' " — it does not remove control
// characters that are outright illegal in XML 1.0 (e.g. PDF text extraction
// can surface raw 0x01 bytes from malformed fonts), which would otherwise
// produce a document.xml/section0.xml that strict parsers (Word, Hancom)
// reject. Strip those first; the common clean-text path allocates nothing extra.
func escapeXMLText(s string) string {
	if strings.IndexFunc(s, isIllegalXMLRune) != -1 {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			if !isIllegalXMLRune(r) {
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// isIllegalXMLRune reports whether r is not a valid XML 1.0 character.
// Legal: U+0009, U+000A, U+000D, U+0020-U+D7FF, U+E000-U+FFFD, U+10000-U+10FFFF.
func isIllegalXMLRune(r rune) bool {
	switch {
	case r == 0x09 || r == 0x0A || r == 0x0D:
		return false
	case r >= 0x20 && r <= 0xD7FF:
		return false
	case r >= 0xE000 && r <= 0xFFFD:
		return false
	case r >= 0x10000 && r <= 0x10FFFF:
		return false
	default:
		return true
	}
}

// DocxToHwpx converts a .docx file to .hwpx, preserving character and
// paragraph formatting via the shared document model.
func DocxToHwpx(data []byte) ([]byte, error) {
	d, err := parseDocx(data)
	if err != nil {
		return nil, err
	}
	return writeHwpx(d), nil
}

// HwpxToDocx converts a .hwpx file to .docx, preserving character and
// paragraph formatting via the shared document model.
func HwpxToDocx(data []byte) ([]byte, error) {
	d, err := parseHwpx(data)
	if err != nil {
		return nil, err
	}
	return writeDocx(d), nil
}
