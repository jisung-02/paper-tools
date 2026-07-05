package pdf

import (
	"bytes"
	"fmt"
	"math"
)

// Interleave alternates pages from a and b (a1 b1 a2 b2 ...); leftover pages
// are appended. reverseB reverses b's page order first (scanned back sides).
func Interleave(a, b []byte, reverseB bool) ([]byte, error) {
	da, err := Parse(a)
	if err != nil {
		return nil, err
	}
	db, err := Parse(b)
	if err != nil {
		return nil, err
	}
	pa, err := da.Pages()
	if err != nil {
		return nil, err
	}
	pb, err := db.Pages()
	if err != nil {
		return nil, err
	}
	if reverseB {
		for i, j := 0, len(pb)-1; i < j; i, j = i+1, j-1 {
			pb[i], pb[j] = pb[j], pb[i]
		}
	}

	docs := []*Doc{da, db}
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	order := make([]pageSel, 0, len(pa)+len(pb))
	for i := 0; i < n; i++ {
		if i < len(pa) {
			order = append(order, pageSel{doc: 0, pg: pa[i]})
		}
		if i < len(pb) {
			order = append(order, pageSel{doc: 1, pg: pb[i]})
		}
	}

	bld, root, err := buildOrdered(docs, order, nil)
	if err != nil {
		return nil, err
	}
	return bld.bytes(root), nil
}

// Resize scales the selected pages ("" = all) to fit inside w x h points,
// centered. Annotations are dropped from resized pages.
func Resize(file []byte, ranges string, w, h float64) ([]byte, error) {
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
		sw, sh := x1-x0, y1-y0
		s := math.Min(w/sw, h/sh)
		tx := (w-s*sw)/2 - s*x0
		ty := (h-s*sh)/2 - s*y0

		pre := []byte(fmt.Sprintf("q %.4f 0 0 %.4f %.2f %.2f cm\n", s, s, tx, ty))
		b.wrapContent(pd, pre, []byte("Q\n"))

		pd["MediaBox"] = Array{0, 0, w, h}
		delete(pd, "CropBox")
		// ponytail: annotation rects don't survive scaling; drop them
		delete(pd, "Annots")
		return nil
	}

	return buildWith([]*Doc{d}, [][]Page{pages}, mut)
}

// NUp places per (2 or 4) consecutive pages onto one A4 sheet
// (landscape for 2-up, portrait for 4-up), 12pt gutter.
func NUp(file []byte, per int) ([]byte, error) {
	if per != 2 && per != 4 {
		return nil, fmt.Errorf("per must be 2 or 4")
	}
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}

	b := &builder{}
	catalogRef := b.alloc()
	pagesRef := b.alloc()

	nums := make([]int, len(pages))
	for i, pg := range pages {
		nums[i] = pg.Num
	}
	m := b.importDoc(d, nums)

	type form struct {
		ref            Ref
		x0, y0, x1, y1 float64
		rotate         int // effective /Rotate: 0, 90, 180, or 270
	}
	forms := make([]form, len(pages))
	for i, pg := range pages {
		nr, ok := m[pg.Num]
		if !ok {
			return nil, fmt.Errorf("page object %d not imported", pg.Num)
		}
		pd, ok := b.objs[nr.Num-1].(Dict)
		if !ok {
			return nil, fmt.Errorf("page object %d is not a dict", pg.Num)
		}
		for k, v := range pg.Attrs {
			if _, exists := pd[k]; !exists {
				pd[k] = translate(v, m)
			}
		}
		if _, ok := pd["MediaBox"]; !ok {
			pd["MediaBox"] = Array{0, 0, 612, 792}
		}

		x0, y0, x1, y1, ok := b.rect(pd["CropBox"])
		if !ok {
			x0, y0, x1, y1, ok = b.rect(pd["MediaBox"])
		}
		if !ok {
			x0, y0, x1, y1 = 0, 0, 612, 792
		}

		// pd["Rotate"] is now the fully resolved effective rotation: the
		// gap-fill loop above already copied it down from pg.Attrs (which
		// Pages() resolves through the ancestor chain) whenever the page
		// itself didn't set /Rotate directly.
		rotate := 0
		if v, ok := rnum(b.rv(pd["Rotate"])); ok {
			rotate = ((int(v) % 360) + 360) % 360
			if rotate != 90 && rotate != 180 && rotate != 270 {
				rotate = 0
			}
		}

		var parts [][]byte
		switch c := b.rv(pd["Contents"]).(type) {
		case *Stream:
			data, err := decodeStreamWith(b.rv, c)
			if err != nil {
				return nil, err
			}
			parts = append(parts, data)
		case Array:
			for _, e := range c {
				st, ok := b.rv(e).(*Stream)
				if !ok {
					continue
				}
				data, err := decodeStreamWith(b.rv, st)
				if err != nil {
					return nil, err
				}
				parts = append(parts, data)
			}
		}
		content := bytes.Join(parts, []byte("\n"))
		comp := zlibDefault(content)

		formDict := Dict{
			"Type":      Name("XObject"),
			"Subtype":   Name("Form"),
			"BBox":      Array{x0, y0, x1, y1},
			"Resources": pd["Resources"],
			"Filter":    Name("FlateDecode"),
			"Length":    len(comp),
		}
		if mat := rotateMatrix(rotate, x0, y0, x1, y1); mat != nil {
			formDict["Matrix"] = mat
		}
		formRef := b.alloc()
		b.objs[formRef.Num-1] = &Stream{Dict: formDict, Data: comp}
		forms[i] = form{ref: formRef, x0: x0, y0: y0, x1: x1, y1: y1, rotate: rotate}

		// the original page shell is no longer needed: its content and
		// resources now live in the Form XObject above.
		b.objs[nr.Num-1] = nil
	}

	var sheetW, sheetH float64
	var cols, rows int
	if per == 2 {
		sheetW, sheetH = a4Height, a4Width // landscape
		cols, rows = 2, 1
	} else {
		sheetW, sheetH = a4Width, a4Height // portrait
		cols, rows = 2, 2
	}
	const gutter = 12.0
	cellW := (sheetW - float64(cols+1)*gutter) / float64(cols)
	cellH := (sheetH - float64(rows+1)*gutter) / float64(rows)

	var kids Array
	for start := 0; start < len(forms); start += per {
		end := start + per
		if end > len(forms) {
			end = len(forms)
		}
		group := forms[start:end]

		xobjDict := Dict{}
		var ops bytes.Buffer
		for i, f := range group {
			col := i % cols
			row := i / cols
			cellX := gutter + float64(col)*(cellW+gutter)
			cellY := sheetH - gutter - float64(row+1)*cellH - float64(row)*gutter
			bw, bh := f.x1-f.x0, f.y1-f.y0
			if f.rotate == 90 || f.rotate == 270 {
				// rotateMatrix already swaps the form's effective width and
				// height onto the (0,0)-origin box below; the cell fit must
				// use the same swapped aspect so a sideways page is scaled
				// against the cell it will actually occupy once rotated.
				bw, bh = bh, bw
			}
			s := math.Min(cellW/bw, cellH/bh)
			var tx, ty float64
			if f.rotate == 0 {
				tx = cellX + (cellW-s*bw)/2 - s*f.x0
				ty = cellY + (cellH-s*bh)/2 - s*f.y0
			} else {
				// rotateMatrix maps the rotated box's origin to (0,0), so no
				// further offset by x0/y0 is needed here.
				tx = cellX + (cellW-s*bw)/2
				ty = cellY + (cellH-s*bh)/2
			}
			name := fmt.Sprintf("F%d", i)
			xobjDict[Name(name)] = f.ref
			fmt.Fprintf(&ops, "q %.4f 0 0 %.4f %.2f %.2f cm /%s Do Q\n", s, s, tx, ty, name)
		}

		sheetRef := b.alloc()
		contentRef := b.alloc()
		b.objs[contentRef.Num-1] = &Stream{Dict: Dict{"Length": ops.Len()}, Data: ops.Bytes()}
		b.objs[sheetRef.Num-1] = Dict{
			"Type":      Name("Page"),
			"Parent":    pagesRef,
			"MediaBox":  Array{0, 0, sheetW, sheetH},
			"Resources": Dict{"XObject": xobjDict},
			"Contents":  contentRef,
		}
		kids = append(kids, sheetRef)
	}

	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}

	return b.bytes(catalogRef), nil
}

// rotateMatrix returns the Form XObject /Matrix that bakes a page's
// effective /Rotate into its placement so an N-up cell shows the page the
// way a viewer would display it. Content operators and BBox stay expressed
// in the page's own (unrotated) coordinate system; the Matrix maps that
// space onto a box whose origin is (0,0) and whose width/height are
// swapped for a 90/270 degree rotation, matching how the page would look
// once rotated. Returns nil for rotate == 0 (identity, no Matrix needed).
func rotateMatrix(rotate int, x0, y0, x1, y1 float64) Array {
	switch rotate {
	case 90:
		return Array{0.0, -1.0, 1.0, 0.0, -y0, x1}
	case 180:
		return Array{-1.0, 0.0, 0.0, -1.0, x1, y1}
	case 270:
		return Array{0.0, 1.0, -1.0, 0.0, y1, -x0}
	default:
		return nil
	}
}

// InsertBlank inserts count blank pages after page `after` (0 = before page 1).
// The blank pages copy the size of the neighboring page.
func InsertBlank(file []byte, after, count int) ([]byte, error) {
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	if after < 0 || after > len(pages) {
		return nil, fmt.Errorf("after out of bounds (0-%d)", len(pages))
	}
	if count < 1 || count > 100 {
		return nil, fmt.Errorf("count must be between 1 and 100")
	}

	b, catalogRef, err := buildDoc([]*Doc{d}, [][]Page{pages}, nil)
	if err != nil {
		return nil, err
	}
	catalog, ok := b.objs[catalogRef.Num-1].(Dict)
	if !ok {
		return nil, fmt.Errorf("catalog is not a dict")
	}
	pagesRef, ok := catalog["Pages"].(Ref)
	if !ok {
		return nil, fmt.Errorf("catalog missing /Pages")
	}
	pagesDict, ok := b.objs[pagesRef.Num-1].(Dict)
	if !ok {
		return nil, fmt.Errorf("pages node is not a dict")
	}
	kids, ok := pagesDict["Kids"].(Array)
	if !ok {
		return nil, fmt.Errorf("pages node missing /Kids")
	}

	neighborIdx := 0
	if after > 0 {
		neighborIdx = after - 1
	}
	neighborRef, ok := kids[neighborIdx].(Ref)
	if !ok {
		return nil, fmt.Errorf("neighbor kid is not a ref")
	}
	neighborDict, ok := b.objs[neighborRef.Num-1].(Dict)
	if !ok {
		return nil, fmt.Errorf("neighbor page is not a dict")
	}
	x0, y0, x1, y1, ok := b.rect(neighborDict["MediaBox"])
	if !ok {
		x0, y0, x1, y1 = 0, 0, 612, 792
	}
	box := Array{x0, y0, x1, y1}

	blanks := make(Array, count)
	for i := 0; i < count; i++ {
		r := b.alloc()
		b.objs[r.Num-1] = Dict{
			"Type":     Name("Page"),
			"Parent":   pagesRef,
			"MediaBox": box,
		}
		blanks[i] = r
	}

	newKids := make(Array, 0, len(kids)+count)
	newKids = append(newKids, kids[:after]...)
	newKids = append(newKids, blanks...)
	newKids = append(newKids, kids[after:]...)
	pagesDict["Kids"] = newKids
	pagesDict["Count"] = len(newKids)

	return b.bytes(catalogRef), nil
}
