package pdf

import "testing"

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
