package pdf

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// selectedSet returns the 1-based page numbers selected by ranges, or all n
// pages when ranges is empty.
func selectedSet(ranges string, n int) (map[int]bool, error) {
	sel := map[int]bool{}
	if ranges == "" {
		for i := 1; i <= n; i++ {
			sel[i] = true
		}
		return sel, nil
	}
	nums, err := ParseRanges(ranges, n)
	if err != nil {
		return nil, err
	}
	for _, n := range nums {
		sel[n] = true
	}
	return sel, nil
}

// docRect resolves v (an Array of 4 numerics, possibly indirect) against d,
// normalized so x0<x1, y0<y1. ok is false if v isn't a valid 4-element array.
func docRect(d *Doc, v any) (x0, y0, x1, y1 float64, ok bool) {
	arr, isArr := d.R(v).(Array)
	if !isArr || len(arr) != 4 {
		return 0, 0, 0, 0, false
	}
	vals := make([]float64, 4)
	for i, e := range arr {
		n, isNum := rnum(d.R(e))
		if !isNum {
			return 0, 0, 0, 0, false
		}
		vals[i] = n
	}
	x0, y0, x1, y1 = vals[0], vals[1], vals[2], vals[3]
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return x0, y0, x1, y1, true
}

// Rotate adds degrees (must be 90, 180, or 270) to the selected pages.
func Rotate(file []byte, ranges string, degrees int) ([]byte, error) {
	if degrees != 90 && degrees != 180 && degrees != 270 {
		return nil, fmt.Errorf("degrees must be 90, 180, or 270")
	}
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	sel, err := selectedSet(ranges, len(pages))
	if err != nil {
		return nil, err
	}
	for i := range pages {
		if !sel[i+1] {
			continue
		}
		old := 0
		if v, ok := rnum(d.R(pages[i].Attrs["Rotate"])); ok {
			old = int(v)
		}
		pages[i].Force = Dict{"Rotate": ((old+degrees)%360 + 360) % 360}
	}
	return build([]*Doc{d}, [][]Page{pages})
}

// Crop insets the selected pages' CropBox by margins given in points.
func Crop(file []byte, ranges string, left, bottom, right, top float64) ([]byte, error) {
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	sel, err := selectedSet(ranges, len(pages))
	if err != nil {
		return nil, err
	}
	for i := range pages {
		if !sel[i+1] {
			continue
		}
		box := pages[i].Attrs["CropBox"]
		if box == nil {
			box = pages[i].Attrs["MediaBox"]
		}
		x0, y0, x1, y1 := 0.0, 0.0, 612.0, 792.0
		if rx0, ry0, rx1, ry1, ok := docRect(d, box); ok {
			x0, y0, x1, y1 = rx0, ry0, rx1, ry1
		}
		nx0, ny0, nx1, ny1 := x0+left, y0+bottom, x1-right, y1-top
		if nx0 >= nx1 || ny0 >= ny1 {
			return nil, fmt.Errorf("crop margins exceed page size")
		}
		pages[i].Force = Dict{"CropBox": Array{nx0, ny0, nx1, ny1}}
	}
	return build([]*Doc{d}, [][]Page{pages})
}

// WatermarkOpts configures Watermark.
type WatermarkOpts struct {
	Text    string
	FontTTF []byte // optional TrueType font bytes; embedded as a subset for
	// Unicode text. When nil, Text must be Latin-1 (base-14
	// Helvetica is used instead).
	FontSize float64 // 0 -> 48
	Opacity  float64 // 0 -> 0.3 (clamped to (0,1])
	Diagonal bool    // rotate 45 degrees around the page center
}

// Watermark stamps translucent text onto every page.
//
// When opts.FontTTF is provided, it is parsed and subset down to the glyphs
// used by opts.Text and embedded as a CIDFontType2/Identity-H font (same
// mechanics as TextToPDF), so any Unicode text (e.g. Korean) can be used.
// When opts.FontTTF is nil, opts.Text must be Latin-1 and is drawn with the
// shared base-14 Helvetica font as before.
func Watermark(file []byte, opts WatermarkOpts) ([]byte, error) {
	if opts.FontSize == 0 {
		opts.FontSize = 48
	}
	if opts.Opacity <= 0 {
		opts.Opacity = 0.3
	}
	if opts.Opacity > 1 {
		opts.Opacity = 1
	}

	var f *ttfFont // non-nil => embed & draw via Identity-H
	var esc string // set only in the base-14 Latin-1 path
	if len(opts.FontTTF) > 0 {
		var err error
		f, err = parseTTF(opts.FontTTF)
		if err != nil {
			return nil, err
		}
		f.markUsed([]rune(opts.Text)...)
	} else {
		var err error
		esc, err = escapeText(opts.Text)
		if err != nil {
			return nil, err
		}
	}

	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}

	theta := 0.0
	if opts.Diagonal {
		theta = math.Pi / 4
	}
	c, s := math.Cos(theta), math.Sin(theta)

	var w float64
	if f != nil {
		// Sum real per-glyph advance widths now that we have font metrics.
		for _, r := range opts.Text {
			gid, _ := f.gid(r)
			w += float64(f.advance1000(gid)) * opts.FontSize / 1000
		}
	} else {
		// ponytail: 0.5em average glyph width, no metrics table
		w = 0.5 * opts.FontSize * float64(len([]rune(opts.Text)))
	}

	var type0Ref Ref
	embedded := false

	mut := func(b *builder, pageIndex int, pd Dict, m map[int]Ref) error {
		x0, y0, x1, y1, ok := b.rect(pd["CropBox"])
		if !ok {
			x0, y0, x1, y1, ok = b.rect(pd["MediaBox"])
		}
		if !ok {
			x0, y0, x1, y1 = 0, 0, 612, 792
		}
		cx, cy := (x0+x1)/2, (y0+y1)/2
		e := cx - w/2*c
		ty := cy - w/2*s

		res := b.ensureResources(pd)
		nextState, nextFont := 0, 0
		stateName := uniqueResourceName(b.ownedSub(res, "ExtGState"), "GSW", &nextState)
		fontName := uniqueResourceName(b.ownedSub(res, "Font"), "FUW", &nextFont)
		b.ownedSub(res, "ExtGState")[stateName] = b.gstateRef(opts.Opacity)

		var ops string
		if f != nil {
			if !embedded {
				var err error
				type0Ref, err = embedTTF(b, f, []rune(opts.Text))
				if err != nil {
					return err
				}
				embedded = true
			}
			b.ownedSub(res, "Font")[fontName] = type0Ref
			ops = fmt.Sprintf("/%s gs BT /%s %.2f Tf 0.5 g %.2f %.2f %.2f %.2f %.2f %.2f Tm <%X> Tj ET",
				stateName, fontName,
				opts.FontSize, c, s, -s, c, e, ty, f.encode(opts.Text))
		} else {
			b.ownedSub(res, "Font")[fontName] = b.helveticaRef()
			ops = fmt.Sprintf("/%s gs BT /%s %.2f Tf 0.5 g %.2f %.2f %.2f %.2f %.2f %.2f Tm (%s) Tj ET",
				stateName, fontName,
				opts.FontSize, c, s, -s, c, e, ty, esc)
		}
		b.appendContent(pd, []byte(ops))
		return nil
	}
	return buildWith([]*Doc{d}, [][]Page{pages}, mut)
}

// PageNumOpts configures AddPageNumbers.
type PageNumOpts struct {
	Format   string  // "" -> "n / N"; N replaced by page count, then n by page number
	FontSize float64 // 0 -> 11
}

// AddPageNumbers stamps a page-number label at the bottom center of every page.
func AddPageNumbers(file []byte, opts PageNumOpts) ([]byte, error) {
	if opts.Format == "" {
		opts.Format = "n / N"
	}
	if opts.FontSize == 0 {
		opts.FontSize = 11
	}
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	total := strconv.Itoa(len(pages))

	mut := func(b *builder, pageIndex int, pd Dict, m map[int]Ref) error {
		x0, y0, x1, y1, ok := b.rect(pd["CropBox"])
		if !ok {
			x0, y0, x1, y1, ok = b.rect(pd["MediaBox"])
		}
		if !ok {
			x0, y0, x1, y1 = 0, 0, 612, 792
		}
		g := pageVisualGeometry{x0: x0, y0: y0, x1: x1, y1: y1, width: x1 - x0, height: y1 - y0}
		if rv, ok := rnum(pd["Rotate"]); ok {
			g.rotate = ((int(rv) % 360) + 360) % 360
		}
		if g.rotate == 90 || g.rotate == 270 {
			g.width, g.height = g.height, g.width
		}
		cx, _ := g.visualPoint(g.width/2, g.height/2)

		label := strings.ReplaceAll(opts.Format, "N", total)
		label = strings.ReplaceAll(label, "n", strconv.Itoa(pageIndex+1))
		esc, err := escapeText(label)
		if err != nil {
			return err
		}
		x := cx - 0.5*opts.FontSize*float64(len(label))/2
		_, y := g.visualPoint(g.width/2, 30)
		res := b.ensureResources(pd)
		nextFont := 0
		fontName := uniqueResourceName(b.ownedSub(res, "Font"), "FUW", &nextFont)
		b.ownedSub(res, "Font")[fontName] = b.helveticaRef()
		ops := fmt.Sprintf("BT /%s %.2f Tf 0 g 1 0 0 1 %.2f %.2f Tm (%s) Tj ET", fontName, opts.FontSize, x, y, esc)
		b.appendContent(pd, []byte(ops))
		return nil
	}
	return buildWith([]*Doc{d}, [][]Page{pages}, mut)
}

// RemovePages deletes the pages in ranges, keeping the rest in order.
func RemovePages(file []byte, ranges string) ([]byte, error) {
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	remove, err := ParseRanges(ranges, len(pages))
	if err != nil {
		return nil, err
	}
	removeSet := map[int]bool{}
	for _, n := range remove {
		removeSet[n] = true
	}
	var sel []Page
	for i, pg := range pages {
		if !removeSet[i+1] {
			sel = append(sel, pg)
		}
	}
	if len(sel) == 0 {
		return nil, fmt.Errorf("cannot remove every page")
	}
	return build([]*Doc{d}, [][]Page{sel})
}
