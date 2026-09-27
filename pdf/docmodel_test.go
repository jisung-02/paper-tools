package pdf

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestMergeRuns(t *testing.T) {
	got := mergeRuns([]Run{
		{Text: "가", Bold: true}, {Text: "나", Bold: true},
		{Text: ""}, {Text: "다"}, {Text: "라", SizePt: 14},
	})
	want := []Run{{Text: "가나", Bold: true}, {Text: "다"}, {Text: "라", SizePt: 14}}
	if len(got) != len(want) {
		t.Fatalf("got %d runs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("run %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestNormalizeDocSplitsNewlines(t *testing.T) {
	d := &DocModel{Blocks: []Block{
		&Para{Align: AlignCenter, Heading: 2, Runs: []Run{{Text: "one\ntwo", Bold: true}, {Text: " tail"}}},
	}}
	n := normalizeDoc(d)
	if len(n.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(n.Blocks))
	}
	p0, p1 := n.Blocks[0].(*Para), n.Blocks[1].(*Para)
	if p0.Align != AlignCenter || p0.Heading != 2 || p1.Align != AlignCenter || p1.Heading != 2 {
		t.Errorf("para props not preserved across split: %+v %+v", p0, p1)
	}
	if len(p0.Runs) != 1 || p0.Runs[0].Text != "one" || !p0.Runs[0].Bold {
		t.Errorf("p0 runs wrong: %+v", p0.Runs)
	}
	if len(p1.Runs) != 2 || p1.Runs[0].Text != "two" || p1.Runs[1].Text != " tail" {
		t.Errorf("p1 runs wrong: %+v", p1.Runs)
	}
}

func TestDocFromParas(t *testing.T) {
	d := docFromParas([]string{"첫 문단", "", "second"})
	if len(d.Blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(d.Blocks))
	}
	if p := d.Blocks[1].(*Para); len(p.Runs) != 0 {
		t.Errorf("empty para should have no runs: %+v", p.Runs)
	}
	if p := d.Blocks[2].(*Para); p.Runs[0].Text != "second" {
		t.Errorf("got %q", p.Runs[0].Text)
	}
}

func TestTableGridSpans(t *testing.T) {
	// 3-col grid:
	// row0: A(colspan2) B(rowspan2)
	// row1: C D            (B covers col2)
	tbl := &Table{Rows: [][]Cell{
		{{ColSpan: 2}, {RowSpan: 2}},
		{{}, {}},
	}}
	cols, items := tableGrid(tbl)
	if cols != 3 {
		t.Fatalf("cols = %d, want 3", cols)
	}
	type pos struct {
		r, c, w int
		covered bool
	}
	var got []pos
	for _, it := range items {
		got = append(got, pos{it.Row, it.Col, it.W, it.Cell == nil})
	}
	want := []pos{
		{0, 0, 2, false}, {0, 2, 1, false},
		{1, 0, 1, false}, {1, 1, 1, false}, {1, 2, 1, true},
	}
	if len(got) != len(want) {
		t.Fatalf("items = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestNormalizeBlocksRecursesIntoCells(t *testing.T) {
	tbl := &Table{Rows: [][]Cell{{{
		Blocks: []Block{&Para{Runs: []Run{{Text: "a\nb"}}}},
	}}}}
	n := normalizeDoc(&DocModel{Blocks: []Block{tbl}})
	cell := n.Blocks[0].(*Table).Rows[0][0]
	if len(cell.Blocks) != 2 {
		t.Fatalf("newline in cell should split para, got %d blocks", len(cell.Blocks))
	}
	if cell.ColSpan != 1 || cell.RowSpan != 1 {
		t.Errorf("spans not normalized to >=1: %+v", cell)
	}
}

func tinyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageSniffAndPixelSize(t *testing.T) {
	p := tinyPNG(t, 3, 2)
	if m := sniffImageMIME(p); m != "image/png" {
		t.Errorf("png sniff: %q", m)
	}
	if w, h, ok := imagePixelSize(p); !ok || w != 3 || h != 2 {
		t.Errorf("png size: %d x %d ok=%v", w, h, ok)
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, image.NewNRGBA(image.Rect(0, 0, 4, 5)), nil); err != nil {
		t.Fatal(err)
	}
	if m := sniffImageMIME(jb.Bytes()); m != "image/jpeg" {
		t.Errorf("jpeg sniff: %q", m)
	}
	if w, h, ok := imagePixelSize(jb.Bytes()); !ok || w != 4 || h != 5 {
		t.Errorf("jpeg size: %d x %d ok=%v", w, h, ok)
	}
	if m := sniffImageMIME([]byte("GIF89a...")); m != "" {
		t.Errorf("gif should be unsupported, got %q", m)
	}
}

func TestImageDisplaySize(t *testing.T) {
	im := &Image{Data: tinyPNG(t, 96, 48)}
	w, h := im.displaySizePt()
	if w != 72 || h != 36 { // 96px @96dpi = 72pt
		t.Errorf("derived size: %v x %v", w, h)
	}
	im2 := &Image{WPt: 100, HPt: 50, Data: im.Data}
	if w, h := im2.displaySizePt(); w != 100 || h != 50 {
		t.Errorf("explicit size ignored: %v x %v", w, h)
	}
}
