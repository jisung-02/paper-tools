package pdf

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// ponytail: text + paragraph breaks only; inline bold/size/color/images/columns/tables-layout are dropped (best-effort reflow, not a faithful renderer).

// DocxText extracts plain text from a .docx file.
func DocxText(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("유효한 docx 파일이 아닙니다")
	}
	if err := validateOfficeZIP(r, "docx"); err != nil {
		return "", err
	}

	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", errors.New("유효한 docx 파일이 아닙니다")
	}

	docBytes, err := readOfficeEntry(docFile, "docx")
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	decoder := xml.NewDecoder(bytes.NewReader(docBytes))
	for {
		tok, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				// Text element: read the next CharData token(s)
			readText:
				for {
					tok, err := decoder.Token()
					if err != nil {
						if err == io.EOF {
							break readText
						}
						return "", err
					}
					switch t2 := tok.(type) {
					case xml.CharData:
						sb.WriteString(string(t2))
						if sb.Len() > int(officeParseLimits.maxTextBytes) {
							return "", errors.New("docx: extracted text too large")
						}
					case xml.EndElement:
						if t2.Name.Local == "t" {
							break readText
						}
					}
				}
			case "tab":
				sb.WriteString("\t")
			case "br", "cr":
				sb.WriteString("\n")
			}
		case xml.EndElement:
			if t.Name.Local == "p" {
				sb.WriteString("\n")
				if sb.Len() > int(officeParseLimits.maxTextBytes) {
					return "", errors.New("docx: extracted text too large")
				}
			}
		}
	}

	text := sb.String()
	// Collapse 3+ consecutive newlines to exactly 2
	re := regexp.MustCompile("\\n{3,}")
	text = re.ReplaceAllString(text, "\n\n")

	return text, nil
}

// DocxToPDF converts a .docx file to PDF.
func DocxToPDF(data []byte, fontTTF []byte, opts TextPDFOpts) ([]byte, error) {
	txt, err := DocxText(data)
	if err != nil {
		return nil, err
	}
	return TextToPDF(txt, fontTTF, opts)
}
