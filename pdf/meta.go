package pdf

import (
	"bytes"
	"errors"
	"math"
	"unicode/utf16"
)

// DocInfo holds document metadata fields for SetMetadata.
type DocInfo struct {
	Title, Author, Subject, Keywords, Creator, Producer string
}

var infoKeys = []Name{"Title", "Author", "Subject", "Keywords", "Creator", "Producer"}

func infoField(info DocInfo, key Name) string {
	switch key {
	case "Title":
		return info.Title
	case "Author":
		return info.Author
	case "Subject":
		return info.Subject
	case "Keywords":
		return info.Keywords
	case "Creator":
		return info.Creator
	case "Producer":
		return info.Producer
	}
	return ""
}

// decodeInfoString decodes a PDF text string: UTF-16BE with a BOM, or Latin-1.
func decodeInfoString(s String) string {
	if len(s) >= 2 && s[0] == 0xFE && s[1] == 0xFF {
		u := s[2:]
		units := make([]uint16, len(u)/2)
		for i := range units {
			units[i] = uint16(u[2*i])<<8 | uint16(u[2*i+1])
		}
		return string(utf16.Decode(units))
	}
	out := make([]rune, len(s))
	for i, c := range s {
		out[i] = rune(c)
	}
	return string(out)
}

// encodeInfoString encodes s as a plain Latin-1 PDF string when every rune
// fits, else as UTF-16BE with a byte-order-mark.
func encodeInfoString(s string) String {
	ascii := true
	for _, r := range s {
		if r >= 128 {
			ascii = false
			break
		}
	}
	if ascii {
		return String(s)
	}
	units := utf16.Encode([]rune(s))
	out := make([]byte, 2+2*len(units))
	out[0], out[1] = 0xFE, 0xFF
	for i, u := range units {
		out[2+2*i] = byte(u >> 8)
		out[2+2*i+1] = byte(u)
	}
	return String(out)
}

// SetMetadata rewrites the document Info dictionary. Non-empty fields
// override; empty fields keep the existing value. stripAll drops the Info
// dict and page-level metadata entirely (fields ignored).
func SetMetadata(file []byte, info DocInfo, stripAll bool) ([]byte, error) {
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}

	existing, _ := d.R(d.trailer["Info"]).(Dict)

	var mut pageMutator
	if stripAll {
		mut = func(b *builder, pageIndex int, pd Dict, m map[int]Ref) error {
			delete(pd, "Metadata")
			delete(pd, "PieceInfo")
			delete(pd, "Thumb")
			return nil
		}
	}

	b, catalogRef, err := buildDoc([]*Doc{d}, [][]Page{pages}, mut)
	if err != nil {
		return nil, err
	}

	if !stripAll {
		merged := Dict{}
		for _, key := range infoKeys {
			val := infoField(info, key)
			if val == "" {
				if es, ok := existing[key].(String); ok {
					val = decodeInfoString(es)
				}
			}
			if val != "" {
				merged[key] = encodeInfoString(val)
			}
		}
		infoRef := b.alloc()
		b.objs[infoRef.Num-1] = merged
		b.infoRef = infoRef
	}

	return b.bytes(catalogRef), nil
}

// GetInfo returns document facts as a JSON-encodable map. Never writes.
func GetInfo(file []byte) (map[string]any, error) {
	d, err := Parse(file)
	if err != nil {
		if errors.Is(err, ErrEncrypted) {
			return map[string]any{
				"encrypted": true,
				"fileSize":  len(file),
				"version":   pdfVersion(file),
			}, nil
		}
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"pages":     len(pages),
		"version":   pdfVersion(file),
		"fileSize":  len(file),
		"encrypted": false,
	}

	if info, ok := d.R(d.trailer["Info"]).(Dict); ok {
		set := func(key Name, jsonKey string) {
			s, ok := info[key].(String)
			if !ok {
				return
			}
			if v := decodeInfoString(s); v != "" {
				out[jsonKey] = v
			}
		}
		set("Title", "title")
		set("Author", "author")
		set("Subject", "subject")
		set("Keywords", "keywords")
		set("Creator", "creator")
		set("Producer", "producer")
	}

	sizes := make([]map[string]any, len(pages))
	for i, pg := range pages {
		x0, y0, x1, y1, ok := docRect(d, pg.Attrs["MediaBox"])
		if !ok {
			x0, y0, x1, y1 = 0, 0, 612, 792
		}
		rotate := 0
		if v, ok := rnum(d.R(pg.Attrs["Rotate"])); ok {
			rotate = int(v)
		}
		sizes[i] = map[string]any{
			"w":      round2(x1 - x0),
			"h":      round2(y1 - y0),
			"rotate": rotate,
		}
	}
	out["pageSizes"] = sizes

	return out, nil
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// pdfVersion scans data's header for "%PDF-" and returns the version string
// that follows, e.g. "1.7".
func pdfVersion(data []byte) string {
	n := len(data)
	if n > 1024 {
		n = 1024
	}
	head := data[:n]
	idx := bytes.Index(head, []byte("%PDF-"))
	if idx < 0 {
		return ""
	}
	start := idx + len("%PDF-")
	end := start
	for end < len(head) && (head[end] == '.' || head[end] >= '0' && head[end] <= '9') {
		end++
	}
	return string(head[start:end])
}
