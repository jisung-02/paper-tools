package pdf

import (
	"bytes"
	"strings"
	"testing"
)

func TestGC(t *testing.T) {
	out, err := Split(classicPDF(), "1")
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if bytes.Contains(out, []byte("/Rotate 90")) {
		t.Fatalf("output should not contain the unselected /Rotate 90 page:\n%s", out)
	}
}

func TestRotate(t *testing.T) {
	out, err := Rotate(classicPDF(), "2", 90)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
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
	pd1, ok := d.Get(pages[0].Num).(Dict)
	if !ok {
		t.Fatalf("page1 is not a dict")
	}
	if v, ok := pd1["Rotate"]; ok {
		if f, _ := rnum(v); f != 0 {
			t.Errorf("page1 Rotate = %v, want absent or 0", v)
		}
	}
	pd2, ok := d.Get(pages[1].Num).(Dict)
	if !ok {
		t.Fatalf("page2 is not a dict")
	}
	f, ok := rnum(pd2["Rotate"])
	if !ok || f != 180 {
		t.Fatalf("page2 Rotate = %v, want 180", pd2["Rotate"])
	}
}

func TestRotateAll(t *testing.T) {
	out, err := Rotate(classicPDF(), "", 90)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	pd1 := d.Get(pages[0].Num).(Dict)
	if f, _ := rnum(pd1["Rotate"]); f != 90 {
		t.Errorf("page1 Rotate = %v, want 90", pd1["Rotate"])
	}
	pd2 := d.Get(pages[1].Num).(Dict)
	if f, _ := rnum(pd2["Rotate"]); f != 180 {
		t.Errorf("page2 Rotate = %v, want 180", pd2["Rotate"])
	}
}

func TestCrop(t *testing.T) {
	out, err := Crop(classicPDF(), "1", 10, 10, 10, 10)
	if err != nil {
		t.Fatalf("Crop: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	pd1 := d.Get(pages[0].Num).(Dict)
	arr, ok := d.R(pd1["CropBox"]).(Array)
	if !ok || len(arr) != 4 {
		t.Fatalf("page1 CropBox = %v", pd1["CropBox"])
	}
	want := []float64{10, 10, 602, 782}
	for i, w := range want {
		if got, _ := rnum(arr[i]); got != w {
			t.Errorf("CropBox[%d] = %v want %v", i, got, w)
		}
	}
	pd2 := d.Get(pages[1].Num).(Dict)
	if _, ok := pd2["CropBox"]; ok {
		t.Errorf("page2 CropBox should be unchanged (absent), got %v", pd2["CropBox"])
	}
}

func TestCropTooBig(t *testing.T) {
	if _, err := Crop(classicPDF(), "", 400, 0, 400, 0); err == nil {
		t.Fatalf("expected error for oversized crop margins")
	}
}

func TestWatermark(t *testing.T) {
	out, err := Watermark(classicPDF(), WatermarkOpts{Text: "DRAFT"})
	if err != nil {
		t.Fatalf("Watermark: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, pg := range pages {
		pd := d.Get(pg.Num).(Dict)
		contents, ok := d.R(pd["Contents"]).(Array)
		if !ok || len(contents) == 0 {
			t.Fatalf("page %d Contents = %v", pg.Num, pd["Contents"])
		}
		res, ok := d.R(pd["Resources"]).(Dict)
		if !ok {
			t.Fatalf("page %d missing Resources", pg.Num)
		}
		font, ok := d.R(res["Font"]).(Dict)
		if !ok || font["FUW0"] == nil {
			t.Fatalf("page %d missing Font/FUW0", pg.Num)
		}
		gs, ok := d.R(res["ExtGState"]).(Dict)
		if !ok || gs["GSW0"] == nil {
			t.Fatalf("page %d missing ExtGState/GSW0", pg.Num)
		}
		last := d.R(contents[len(contents)-1])
		st, ok := last.(*Stream)
		if !ok {
			t.Fatalf("page %d last content is not a stream", pg.Num)
		}
		if !bytes.Contains(st.Data, []byte("(DRAFT) Tj")) {
			t.Errorf("page %d last content stream missing watermark text: %s", pg.Num, st.Data)
		}
	}
}

func TestWatermarkKorean(t *testing.T) {
	_, err := Watermark(classicPDF(), WatermarkOpts{Text: "초안"})
	if err == nil {
		t.Fatalf("expected error for non-Latin-1 text")
	}
	if !strings.Contains(err.Error(), "Latin-1") {
		t.Errorf("error should mention Latin-1, got: %v", err)
	}
}

func TestPageNumbers(t *testing.T) {
	out, err := AddPageNumbers(classicPDF(), PageNumOpts{})
	if err != nil {
		t.Fatalf("AddPageNumbers: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	want := []string{"(1 / 2) Tj", "(2 / 2) Tj"}
	for i, pg := range pages {
		pd := d.Get(pg.Num).(Dict)
		contents := d.R(pd["Contents"]).(Array)
		last := d.R(contents[len(contents)-1]).(*Stream)
		if !bytes.Contains(last.Data, []byte(want[i])) {
			t.Errorf("page %d content missing %q: %s", i+1, want[i], last.Data)
		}
	}
}

func TestRemovePages(t *testing.T) {
	out, err := RemovePages(classicPDF(), "1")
	if err != nil {
		t.Fatalf("RemovePages: %v", err)
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
	pd := d.Get(pages[0].Num).(Dict)
	if f, _ := rnum(pd["Rotate"]); f != 90 {
		t.Errorf("remaining page Rotate = %v, want 90", pd["Rotate"])
	}

	if _, err := RemovePages(classicPDF(), "1-2"); err == nil {
		t.Fatalf("expected error removing every page")
	}
}
