package pdf

import (
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
