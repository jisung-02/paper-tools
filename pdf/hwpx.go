package pdf

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// ponytail: text + paragraph breaks only (t/p/tab elements); formatting/layout/images/line-break details are dropped (best-effort reflow, not a faithful renderer).

// HwpxText extracts plain text from a .hwpx file (ZIP archive containing HWPML XML sections).
// Returns text from all section*.xml entries in sorted order, with newlines for paragraph breaks
// and tabs for tab elements. Collapses runs of 3+ newlines to exactly 2.
func HwpxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("hwpx: invalid zip file: %v", err)
	}

	// Collect all section*.xml entries.
	var sectionEntries []*zip.File
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "Contents/section") && strings.HasSuffix(f.Name, ".xml") {
			sectionEntries = append(sectionEntries, f)
		}
	}
	if len(sectionEntries) == 0 {
		return "", errors.New("유효한 hwpx 파일이 아닙니다")
	}

	// Sort by Name.
	sort.Slice(sectionEntries, func(i, j int) bool {
		return sectionEntries[i].Name < sectionEntries[j].Name
	})

	var allText strings.Builder
	for _, f := range sectionEntries {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		sectionText := extractSectionText(rc)
		allText.WriteString(sectionText)
		rc.Close()
	}

	// Collapse 3+ consecutive newlines to exactly 2.
	result := allText.String()
	re := regexp.MustCompile(`\n{3,}`)
	result = re.ReplaceAllString(result, "\n\n")

	return result, nil
}

// extractSectionText uses an XML Decoder to stream-parse a section file,
// extracting text from t, p, and tab elements.
func extractSectionText(r io.Reader) string {
	var text strings.Builder
	dec := xml.NewDecoder(r)

	// inT tracks whether we are currently inside a <t> element; only CharData
	// inside <t> is real paragraph text (CharData elsewhere is inter-element
	// whitespace/indentation in pretty-printed files).
	inT := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inT = true
			case "tab":
				text.WriteString("\t")
			}
		case xml.CharData:
			if inT {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "p":
				text.WriteString("\n")
			}
		}
	}
	return text.String()
}

// HwpxToPDF converts a .hwpx file to a PDF by extracting text and rendering it.
func HwpxToPDF(data []byte, fontTTF []byte, opts TextPDFOpts) ([]byte, error) {
	txt, err := HwpxText(data)
	if err != nil {
		return nil, err
	}
	return TextToPDF(txt, fontTTF, opts)
}
