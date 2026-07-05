package pdf

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pageMediaBox resolves a page dict's own /MediaBox (must be present, not
// inherited) to a [x0 y0 x1 y1] slice of float64.
func pageMediaBox(t *testing.T, d *Doc, pg Page) [4]float64 {
	t.Helper()
	pd, ok := d.Get(pg.Num).(Dict)
	if !ok {
		t.Fatalf("page %d is not a dict", pg.Num)
	}
	x0, y0, x1, y1, ok := docRect(d, pd["MediaBox"])
	if !ok {
		t.Fatalf("page %d missing MediaBox", pg.Num)
	}
	return [4]float64{x0, y0, x1, y1}
}

func approxEq(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestInterleave(t *testing.T) {
	out, err := Interleave(classicPDF(), xrefStreamPDF(), false)
	if err != nil {
		t.Fatalf("Interleave: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(pages))
	}
	want := []float64{612, 100, 612}
	for i, w := range want {
		box := pageMediaBox(t, d, pages[i])
		if got := box[2] - box[0]; got != w {
			t.Errorf("page %d width = %v want %v", i, got, w)
		}
	}
}

func TestInterleaveReverse(t *testing.T) {
	out, err := Interleave(classicPDF(), classicPDF(), true)
	if err != nil {
		t.Fatalf("Interleave: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 4 {
		t.Fatalf("expected 4 pages, got %d", len(pages))
	}
	pd, ok := d.Get(pages[1].Num).(Dict)
	if !ok {
		t.Fatalf("page 2 is not a dict")
	}
	if f, ok := rnum(pd["Rotate"]); !ok || f != 90 {
		t.Fatalf("page 2 Rotate = %v, want 90", pd["Rotate"])
	}
}

func TestResize(t *testing.T) {
	out, err := Resize(classicPDF(), "", 595.28, 841.89)
	if err != nil {
		t.Fatalf("Resize: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	for i, pg := range pages {
		box := pageMediaBox(t, d, pg)
		want := [4]float64{0, 0, 595.28, 841.89}
		for j := range want {
			if !approxEq(box[j], want[j], 0.01) {
				t.Fatalf("page %d MediaBox = %v want %v", i, box, want)
			}
		}
	}

	pd, ok := d.Get(pages[0].Num).(Dict)
	if !ok {
		t.Fatalf("page 0 is not a dict")
	}
	contents, ok := d.R(pd["Contents"]).(Array)
	if !ok || len(contents) < 2 {
		t.Fatalf("Contents = %v, want an Array of >= 2 elements", pd["Contents"])
	}
	st, ok := d.R(contents[0]).(*Stream)
	if !ok {
		t.Fatalf("first content element is not a stream")
	}
	data := string(st.Data)
	if !strings.HasPrefix(data, "q ") {
		t.Errorf("first content stream = %q, want prefix %q", data, "q ")
	}
	if !strings.Contains(data, " cm") {
		t.Errorf("first content stream = %q, want it to contain %q", data, " cm")
	}
}

func TestNUp2(t *testing.T) {
	out, err := NUp(classicPDF(), 2)
	if err != nil {
		t.Fatalf("NUp: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	box := pageMediaBox(t, d, pages[0])
	want := [4]float64{0, 0, 841.89, 595.28}
	for j := range want {
		if !approxEq(box[j], want[j], 0.01) {
			t.Fatalf("MediaBox = %v want %v", box, want)
		}
	}

	pd, ok := d.Get(pages[0].Num).(Dict)
	if !ok {
		t.Fatalf("page is not a dict")
	}
	st, ok := d.R(pd["Contents"]).(*Stream)
	if !ok {
		t.Fatalf("Contents is not a stream")
	}
	data := string(st.Data)
	if !strings.Contains(data, "/F0 Do") {
		t.Errorf("content = %q, want it to contain /F0 Do", data)
	}
	if !strings.Contains(data, "/F1 Do") {
		t.Errorf("content = %q, want it to contain /F1 Do", data)
	}

	res, ok := d.R(pd["Resources"]).(Dict)
	if !ok {
		t.Fatalf("Resources is not a dict")
	}
	xobj, ok := d.R(res["XObject"]).(Dict)
	if !ok {
		t.Fatalf("Resources/XObject is not a dict")
	}
	f0, ok := d.R(xobj["F0"]).(*Stream)
	if !ok {
		t.Fatalf("XObject/F0 does not resolve to a stream")
	}
	if f0.Dict["Subtype"] != Name("Form") {
		t.Errorf("F0 Subtype = %v, want Form", f0.Dict["Subtype"])
	}
}

func TestNUp4(t *testing.T) {
	out, err := NUp(classicPDF(), 4)
	if err != nil {
		t.Fatalf("NUp: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 sheet, got %d", len(pages))
	}
	box := pageMediaBox(t, d, pages[0])
	want := [4]float64{0, 0, 595.28, 841.89}
	for j := range want {
		if !approxEq(box[j], want[j], 0.01) {
			t.Fatalf("MediaBox = %v want %v", box, want)
		}
	}
}

// rotatedPDF builds a PDF with four leaf pages exercising every rotation
// NUp must bake in: page A has no rotation, page B sets /Rotate 90
// directly, page C sets /Rotate 270 directly, and page D sets no /Rotate
// of its own but sits under an intermediate Pages node (object 7) that
// sets /Rotate 180, so D's effective rotation is purely inherited.
func rotatedPDF() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	offsets := make([]int, 8) // index 1..7
	writeObjRaw := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeObjRaw(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObjRaw(2, "<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R 7 0 R] /Count 4 /MediaBox [0 0 300 200] >>")
	writeObjRaw(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] >>")
	writeObjRaw(4, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 150 300] /Rotate 90 >>")
	writeObjRaw(5, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 120 80] /Rotate 270 >>")
	writeObjRaw(6, "<< /Type /Page /Parent 7 0 R /MediaBox [0 0 100 50] >>") // inherits /Rotate 180 from object 7
	writeObjRaw(7, "<< /Type /Pages /Parent 2 0 R /Kids [6 0 R] /Count 1 /Rotate 180 >>")

	xrefOff := buf.Len()
	buf.WriteString("xref\n0 8\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Root 1 0 R /Size 8 >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOff)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

// TestNUpRotate checks that NUp bakes each source page's effective /Rotate
// (own, or inherited from an ancestor Pages node) into its placement, so
// the sheet shows every page the way a viewer honoring /Rotate would: full
// 90/180/270 turn, no distortion, and scaled to fill its cell.
func TestNUpRotate(t *testing.T) {
	out, err := NUp(rotatedPDF(), 4)
	if err != nil {
		t.Fatalf("NUp: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 sheet, got %d", len(pages))
	}
	pd, ok := d.Get(pages[0].Num).(Dict)
	if !ok {
		t.Fatalf("page is not a dict")
	}
	st, ok := d.R(pd["Contents"]).(*Stream)
	if !ok {
		t.Fatalf("Contents is not a stream")
	}
	data := string(st.Data)
	res, ok := d.R(pd["Resources"]).(Dict)
	if !ok {
		t.Fatalf("Resources is not a dict")
	}
	xobj, ok := d.R(res["XObject"]).(Dict)
	if !ok {
		t.Fatalf("Resources/XObject is not a dict")
	}

	const gutter = 12.0
	sheetW, sheetH := a4Width, a4Height // per=4 -> portrait, 2x2 grid
	cols, rows := 2, 2
	cellW := (sheetW - float64(cols+1)*gutter) / float64(cols)
	cellH := (sheetH - float64(rows+1)*gutter) / float64(rows)

	type want struct {
		rotate         int
		x0, y0, x1, y1 float64 // source page box (form BBox)
		col, row       int
	}
	wants := []want{
		{0, 0, 0, 200, 100, 0, 0},
		{90, 0, 0, 150, 300, 1, 0},
		{270, 0, 0, 120, 80, 0, 1},
		{180, 0, 0, 100, 50, 1, 1},
	}

	re := regexp.MustCompile(`q ([\-0-9.]+) 0 0 ([\-0-9.]+) ([\-0-9.]+) ([\-0-9.]+) cm /(F\d+) Do Q`)
	matches := re.FindAllStringSubmatch(data, -1)
	if len(matches) != len(wants) {
		t.Fatalf("content stream = %q, want %d placement ops, got %d", data, len(wants), len(matches))
	}

	for i, w := range wants {
		mm := matches[i]
		sx, _ := strconv.ParseFloat(mm[1], 64)
		sy, _ := strconv.ParseFloat(mm[2], 64)
		tx, _ := strconv.ParseFloat(mm[3], 64)
		ty, _ := strconv.ParseFloat(mm[4], 64)
		formName := Name(mm[5])
		if !approxEq(sx, sy, 1e-6) {
			t.Fatalf("form %d: non-uniform scale %v x %v (would distort)", i, sx, sy)
		}

		f, ok := d.R(xobj[formName]).(*Stream)
		if !ok {
			t.Fatalf("XObject/%s does not resolve to a stream", formName)
		}
		bx0, by0, bx1, by1, ok := docRect(d, f.Dict["BBox"])
		if !ok {
			t.Fatalf("form %d BBox missing", i)
		}
		if !approxEq(bx0, w.x0, 0.01) || !approxEq(by0, w.y0, 0.01) || !approxEq(bx1, w.x1, 0.01) || !approxEq(by1, w.y1, 0.01) {
			t.Fatalf("form %d BBox = [%v %v %v %v], want [%v %v %v %v]", i, bx0, by0, bx1, by1, w.x0, w.y0, w.x1, w.y1)
		}

		mv, hasMatrix := f.Dict["Matrix"].(Array)
		if w.rotate == 0 {
			if hasMatrix {
				t.Fatalf("form %d should not carry /Matrix for rotate=0, got %v", i, mv)
			}
		} else if !hasMatrix || len(mv) != 6 {
			t.Fatalf("form %d Matrix missing or malformed: %v", i, f.Dict["Matrix"])
		}

		// Apply Matrix (or identity, when absent) to the four BBox corners,
		// then apply the outer "cm" scale/translate, to get the final
		// sheet-space quadrilateral this page occupies.
		a, bcoef, c, dcoef, e, fF := 1.0, 0.0, 0.0, 1.0, 0.0, 0.0
		if hasMatrix {
			a, bcoef, c, dcoef, e, fF = toFloat(mv[0]), toFloat(mv[1]), toFloat(mv[2]), toFloat(mv[3]), toFloat(mv[4]), toFloat(mv[5])
		}
		corners := [4][2]float64{{bx0, by0}, {bx1, by0}, {bx0, by1}, {bx1, by1}}
		var minX, minY, maxX, maxY float64
		for ci, p := range corners {
			ix := a*p[0] + c*p[1] + e
			iy := bcoef*p[0] + dcoef*p[1] + fF
			fx := sx*ix + tx
			fy := sy*iy + ty
			if ci == 0 {
				minX, maxX, minY, maxY = fx, fx, fy, fy
				continue
			}
			if fx < minX {
				minX = fx
			}
			if fx > maxX {
				maxX = fx
			}
			if fy < minY {
				minY = fy
			}
			if fy > maxY {
				maxY = fy
			}
		}
		gotW, gotH := maxX-minX, maxY-minY

		// The rendered rectangle's aspect must match how a viewer would
		// display this page: width/height swapped for a 90/270 turn.
		wantW, wantH := w.x1-w.x0, w.y1-w.y0
		if w.rotate == 90 || w.rotate == 270 {
			wantW, wantH = wantH, wantW
		}
		wantAspect := wantW / wantH
		gotAspect := gotW / gotH
		if !approxEq(gotAspect, wantAspect, 1e-3) {
			t.Fatalf("form %d aspect = %v (%vx%v), want %v (%vx%v) — rotation not applied correctly", i, gotAspect, gotW, gotH, wantAspect, wantW, wantH)
		}

		// It must be scaled to fill its cell (touch at least one axis) and
		// stay within it.
		if gotW > cellW+0.01 || gotH > cellH+0.01 {
			t.Fatalf("form %d size %vx%v exceeds cell %vx%v", i, gotW, gotH, cellW, cellH)
		}
		if !approxEq(gotW, cellW, 0.5) && !approxEq(gotH, cellH, 0.5) {
			t.Fatalf("form %d size %vx%v does not fill either cell dimension %vx%v", i, gotW, gotH, cellW, cellH)
		}

		// It must be centered within its target cell.
		cellX := gutter + float64(w.col)*(cellW+gutter)
		cellY := sheetH - gutter - float64(w.row+1)*cellH - float64(w.row)*gutter
		wantCenterX, wantCenterY := cellX+cellW/2, cellY+cellH/2
		gotCenterX, gotCenterY := (minX+maxX)/2, (minY+maxY)/2
		if !approxEq(gotCenterX, wantCenterX, 0.5) || !approxEq(gotCenterY, wantCenterY, 0.5) {
			t.Fatalf("form %d center = (%v,%v), want (%v,%v)", i, gotCenterX, gotCenterY, wantCenterX, wantCenterY)
		}
	}
}

func TestNUpBadPer(t *testing.T) {
	if _, err := NUp(classicPDF(), 3); err == nil {
		t.Fatalf("expected error for per=3")
	}
}

func TestInsertBlank(t *testing.T) {
	out, err := InsertBlank(classicPDF(), 1, 2)
	if err != nil {
		t.Fatalf("InsertBlank: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 4 {
		t.Fatalf("expected 4 pages, got %d", len(pages))
	}
	for _, i := range []int{1, 2} {
		pd, ok := d.Get(pages[i].Num).(Dict)
		if !ok {
			t.Fatalf("page %d is not a dict", i)
		}
		box := pageMediaBox(t, d, pages[i])
		want := [4]float64{0, 0, 612, 792}
		if box != want {
			t.Errorf("page %d MediaBox = %v want %v", i, box, want)
		}
		if c := d.R(pd["Contents"]); c != nil {
			t.Errorf("page %d Contents = %v, want nil", i, c)
		}
	}
}

func TestInsertBlankBounds(t *testing.T) {
	if _, err := InsertBlank(classicPDF(), 5, 1); err == nil {
		t.Fatalf("expected error for out-of-bounds after")
	}
}
