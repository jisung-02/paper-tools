package pdf

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func hwpxEntry(t *testing.T, hwpx []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(hwpx), int64(len(hwpx)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return string(b)
		}
	}
	t.Fatalf("entry %s not found", name)
	return ""
}

func TestWriteHwpxFormatting(t *testing.T) {
	doc := &DocModel{Blocks: []Block{
		&Para{Heading: 1, Runs: []Run{{Text: "제목"}}},
		&Para{Align: AlignCenter, Runs: []Run{
			{Text: "굵은", Bold: true},
			{Text: "빨강", Color: 0xFF0000, SizePt: 14, Italic: true, Underline: true, Strike: true},
			{Text: "또굵은", Bold: true}, // same style as the first run → same charPr id
		}},
		&Para{Runs: []Run{{Text: "탭\t뒤"}}},
	}}
	b := writeHwpx(doc)

	header := hwpxEntry(t, b, "Contents/header.xml")
	for _, want := range []string{
		`<hh:bold/>`, `<hh:italic/>`,
		`<hh:underline type="BOTTOM"`, `<hh:strikeout`,
		`textColor="#FF0000"`, `height="1400"`,
		`horizontal="CENTER"`, `engName="Heading 1"`,
	} {
		if !strings.Contains(header, want) {
			t.Errorf("header.xml missing %s", want)
		}
	}
	// dedup: "굵은" and "또굵은" share one bold charPr → exactly one <hh:bold/>
	if strings.Count(header, "<hh:bold/>") != 2 { // one for bold runs, one for the heading charPr
		t.Errorf("charPr dedup broken: %d bold entries", strings.Count(header, "<hh:bold/>"))
	}
	section := hwpxEntry(t, b, "Contents/section0.xml")
	if !strings.Contains(section, `<hp:tab/>`) {
		t.Errorf("section missing tab element")
	}
	// mimetype must be the first entry, stored uncompressed
	zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
		t.Errorf("mimetype entry wrong: %s method %d", zr.File[0].Name, zr.File[0].Method)
	}
	// text still extractable by the untouched reader
	txt, err := HwpxText(b)
	if err != nil {
		t.Fatalf("HwpxText: %v", err)
	}
	for _, want := range []string{"제목", "굵은", "빨강", "탭\t뒤"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q; got %q", want, txt)
		}
	}
}

func TestHwpxWriteParseRoundTrip(t *testing.T) {
	orig := &DocModel{Blocks: []Block{
		&Para{Heading: 1, Runs: []Run{{Text: "제목 하나"}}},
		&Para{Align: AlignRight, Runs: []Run{
			{Text: "굵은", Bold: true},
			{Text: "빨강 큰", Color: 0xFF0000, SizePt: 14, Italic: true, Underline: true, Strike: true},
		}},
		&Para{},
		&Para{Runs: []Run{{Text: "탭\t뒤 마지막"}}},
	}}
	parsed, err := parseHwpx(writeHwpx(orig))
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	// Heading runs come back with folded bold + headingSizePt (hwpx styles
	// carry no cascade) — adjust expectations accordingly.
	want := &DocModel{Blocks: []Block{
		&Para{Heading: 1, Runs: []Run{{Text: "제목 하나", Bold: true, SizePt: headingSizePt(1)}}},
		orig.Blocks[1], orig.Blocks[2], orig.Blocks[3],
	}}
	assertDocEqual(t, parsed, want)
}

func TestParseHwpxRejectsGarbage(t *testing.T) {
	if _, err := parseHwpx([]byte("not a zip")); err == nil {
		t.Fatal("expected error for non-zip input")
	}
}
