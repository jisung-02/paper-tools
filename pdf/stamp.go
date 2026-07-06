package pdf

import (
	"fmt"
	"strconv"
	"strings"
)

// Position anchors a stamp image to one of nine fixed spots on a page.
type Position string

const (
	PosTopLeft      Position = "top-left"
	PosTopCenter    Position = "top-center"
	PosTopRight     Position = "top-right"
	PosMiddleLeft   Position = "middle-left"
	PosCenter       Position = "center"
	PosMiddleRight  Position = "middle-right"
	PosBottomLeft   Position = "bottom-left"
	PosBottomCenter Position = "bottom-center"
	PosBottomRight  Position = "bottom-right"
)

// StampOpts configures StampImage.
type StampOpts struct {
	Position     Position // "" -> bottom-right
	WidthPercent float64  // stamp width as % of page width, aspect preserved; <=0 -> 20
	MarginPt     float64  // gap in points from the anchor edge(s); <=0 -> 24
	Opacity      float64  // ExtGState /ca /CA; <=0 -> 1 (fully opaque), clamped to (0,1]
	Pages        string   // ParseRanges syntax ("1-3,5"), "first", "last", or "" for all pages
}

// StampTextOpts configures StampText.
type StampTextOpts struct {
	Text     string
	FontTTF  []byte   // optional TrueType font bytes for Unicode text
	Position Position // "" -> bottom-right
	FontSize float64  // <=0 -> 24
	MarginPt float64  // <=0 -> 24
	Opacity  float64  // <=0 -> 1 (fully opaque), clamped to (0,1]
	Pages    string   // ParseRanges syntax ("1-3,5"), "first", "last", or "" for all pages
}

// validPositions enumerates the nine accepted Position values.
var validPositions = map[Position]bool{
	PosTopLeft: true, PosTopCenter: true, PosTopRight: true,
	PosMiddleLeft: true, PosCenter: true, PosMiddleRight: true,
	PosBottomLeft: true, PosBottomCenter: true, PosBottomRight: true,
}

// StampImage overlays imgData (PNG or JPEG) onto the selected pages of
// pdfData, anchored per opts.Position with its aspect ratio preserved. It's
// meant for stamps/signatures: a small image placed at a fixed spot, with
// PNG alpha (via /SMask, see embedPNG) making the surrounding pixels see
// through to the page underneath.
func StampImage(pdfData, imgData []byte, opts StampOpts) ([]byte, error) {
	pos := opts.Position
	if pos == "" {
		pos = PosBottomRight
	}
	if !validPositions[pos] {
		return nil, fmt.Errorf("unknown stamp position %q", pos)
	}
	widthPct := opts.WidthPercent
	if widthPct <= 0 {
		widthPct = 20
	}
	margin := opts.MarginPt
	if margin <= 0 {
		margin = 24
	}
	opacity := opts.Opacity
	if opacity <= 0 {
		opacity = 1
	}
	if opacity > 1 {
		opacity = 1
	}

	d, err := Parse(pdfData)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	sel, err := selectedSet(mapPageAlias(opts.Pages, len(pages)), len(pages))
	if err != nil {
		return nil, err
	}

	// The image is embedded once (lazily, on the first selected page) and
	// its XObject ref reused across every other selected page, the same way
	// Watermark shares its font/gstate refs.
	var xobjRef Ref
	var imgW, imgH int
	embedded := false

	mut := func(b *builder, pageIndex int, pd Dict, m map[int]Ref) error {
		if !sel[pageIndex+1] {
			return nil
		}
		if !embedded {
			ref, w, h, _, err := embedImage(b, imgData, 0)
			if err != nil {
				return err
			}
			xobjRef, imgW, imgH, embedded = ref, w, h, true
		}

		x0, y0, x1, y1, ok := b.rect(pd["CropBox"])
		if !ok {
			x0, y0, x1, y1, ok = b.rect(pd["MediaBox"])
		}
		if !ok {
			x0, y0, x1, y1 = 0, 0, 612, 792
		}
		pageW := x1 - x0

		drawW := widthPct / 100 * pageW
		drawH := drawW * float64(imgH) / float64(imgW)
		x, y := anchorXY(pos, x0, y0, x1, y1, drawW, drawH, margin)

		res := b.ensureResources(pd)
		b.ownedSub(res, "XObject")["StIm0"] = xobjRef
		b.ownedSub(res, "ExtGState")["GSSt0"] = b.gstateRef(opacity)
		ops := fmt.Sprintf("/GSSt0 gs %.2f 0 0 %.2f %.2f %.2f cm /StIm0 Do", drawW, drawH, x, y)
		b.appendContent(pd, []byte(ops))
		return nil
	}

	return buildWith([]*Doc{d}, [][]Page{pages}, mut)
}

// StampText overlays a text label onto selected pages at one of the same fixed
// anchor positions as StampImage. With FontTTF it embeds a small Type0 subset
// so Unicode text, including Korean, can be stamped.
func StampText(pdfData []byte, opts StampTextOpts) ([]byte, error) {
	text := strings.TrimSpace(opts.Text)
	if text == "" {
		return nil, fmt.Errorf("stamp text is empty")
	}
	pos := opts.Position
	if pos == "" {
		pos = PosBottomRight
	}
	if !validPositions[pos] {
		return nil, fmt.Errorf("unknown stamp position %q", pos)
	}
	fontSize := opts.FontSize
	if fontSize <= 0 {
		fontSize = 24
	}
	margin := opts.MarginPt
	if margin <= 0 {
		margin = 24
	}
	opacity := opts.Opacity
	if opacity <= 0 {
		opacity = 1
	}
	if opacity > 1 {
		opacity = 1
	}

	var f *ttfFont
	var esc string
	var width float64
	usedRunes := []rune(text)
	if len(opts.FontTTF) > 0 {
		var err error
		f, err = parseTTF(opts.FontTTF)
		if err != nil {
			return nil, err
		}
		f.markUsed(usedRunes...)
		width = lineWidth(f, usedRunes, fontSize)
	} else {
		var err error
		esc, err = escapeText(text)
		if err != nil {
			return nil, err
		}
		width = 0.5 * fontSize * float64(len(usedRunes))
	}

	d, err := Parse(pdfData)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	sel, err := selectedSet(mapPageAlias(opts.Pages, len(pages)), len(pages))
	if err != nil {
		return nil, err
	}

	var type0Ref Ref
	embedded := false

	mut := func(b *builder, pageIndex int, pd Dict, m map[int]Ref) error {
		if !sel[pageIndex+1] {
			return nil
		}

		x0, y0, x1, y1, ok := b.rect(pd["CropBox"])
		if !ok {
			x0, y0, x1, y1, ok = b.rect(pd["MediaBox"])
		}
		if !ok {
			x0, y0, x1, y1 = 0, 0, 612, 792
		}
		x, y := anchorXY(pos, x0, y0, x1, y1, width, fontSize, margin)

		res := b.ensureResources(pd)
		b.ownedSub(res, "ExtGState")["GSStT0"] = b.gstateRef(opacity)

		var ops string
		if f != nil {
			if !embedded {
				var err error
				type0Ref, err = embedTTF(b, f, usedRunes)
				if err != nil {
					return err
				}
				embedded = true
			}
			b.ownedSub(res, "Font")["FStT0"] = type0Ref
			ops = fmt.Sprintf("/GSStT0 gs BT /FStT0 %.2f Tf 0 g 1 0 0 1 %.2f %.2f Tm <%X> Tj ET",
				fontSize, x, y, f.encode(text))
		} else {
			b.ownedSub(res, "Font")["FStT0"] = b.helveticaRef()
			ops = fmt.Sprintf("/GSStT0 gs BT /FStT0 %.2f Tf 0 g 1 0 0 1 %.2f %.2f Tm (%s) Tj ET",
				fontSize, x, y, esc)
		}
		b.appendContent(pd, []byte(ops))
		return nil
	}

	return buildWith([]*Doc{d}, [][]Page{pages}, mut)
}

// anchorXY returns the lower-left corner at which to place a drawW x drawH
// box anchored to pos within the page rect (x0,y0)-(x1,y1), inset by margin
// from any edge the anchor touches (centered edges/axes ignore margin).
func anchorXY(pos Position, x0, y0, x1, y1, drawW, drawH, margin float64) (x, y float64) {
	switch pos {
	case PosTopLeft:
		x, y = x0+margin, y1-margin-drawH
	case PosTopCenter:
		x, y = (x0+x1)/2-drawW/2, y1-margin-drawH
	case PosTopRight:
		x, y = x1-margin-drawW, y1-margin-drawH
	case PosMiddleLeft:
		x, y = x0+margin, (y0+y1)/2-drawH/2
	case PosCenter:
		x, y = (x0+x1)/2-drawW/2, (y0+y1)/2-drawH/2
	case PosMiddleRight:
		x, y = x1-margin-drawW, (y0+y1)/2-drawH/2
	case PosBottomLeft:
		x, y = x0+margin, y0+margin
	case PosBottomCenter:
		x, y = (x0+x1)/2-drawW/2, y0+margin
	default: // PosBottomRight
		x, y = x1-margin-drawW, y0+margin
	}
	return
}

// mapPageAlias maps the whole-string aliases "first"/"last" (case
// insensitive) to explicit 1-based page numbers for ParseRanges/selectedSet.
// Any other string, including "", passes through unchanged.
func mapPageAlias(s string, n int) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "first":
		return "1"
	case "last":
		return strconv.Itoa(n)
	default:
		return s
	}
}
