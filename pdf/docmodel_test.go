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
