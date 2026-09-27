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

func TestWriteHwpxCharPrCap(t *testing.T) {
	p := &Para{}
	for i := 0; i < maxHwpxCharPrs+10; i++ {
		p.Runs = append(p.Runs, Run{Text: "x", SizePt: 6 + float64(i)/10})
	}
	header := hwpxEntry(t, writeHwpx(&DocModel{Blocks: []Block{p}}), "Contents/header.xml")
	if n := strings.Count(header, "<hh:charPr "); n > maxHwpxCharPrs {
		t.Fatalf("charPr table not capped: %d entries", n)
	}
}

func TestWriteHwpxImage(t *testing.T) {
	img := &Image{Data: tinyPNG(t, 10, 10)}
	doc := &DocModel{Blocks: []Block{&Para{Runs: []Run{{Text: "앞"}}}, img}}
	b := writeHwpx(doc)
	section := hwpxEntry(t, b, "Contents/section0.xml")
	for _, want := range []string{`<hp:pic reverse="0">`, `binaryItemIDRef="image0"`, `width="750" height="750"`} {
		if !strings.Contains(section, want) {
			t.Errorf("section missing %s", want)
		}
	}
	bin := hwpxEntry(t, b, "BinData/image0.png")
	if !strings.HasPrefix(bin, "\x89PNG") {
		t.Errorf("BinData entry not the PNG bytes")
	}
	manifest := hwpxEntry(t, b, "META-INF/manifest.xml")
	if !strings.Contains(manifest, `BinData/image0.png`) {
		t.Errorf("manifest missing BinData entry")
	}
	hpf := hwpxEntry(t, b, "Contents/content.hpf")
	if !strings.Contains(hpf, `id="image0"`) {
		t.Errorf("content.hpf missing item")
	}
}

func TestWriteHwpxTable(t *testing.T) {
	doc := &DocModel{Blocks: []Block{
		&Table{Rows: [][]Cell{
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "머리", Bold: true}}}}, ColSpan: 2},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "세로"}}}}, RowSpan: 2}},
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "한"}}}}},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "둘"}}}}}},
		}},
	}}
	b := writeHwpx(doc)
	header := hwpxEntry(t, b, "Contents/header.xml")
	if !strings.Contains(header, `<hh:borderFills itemCnt="2">`) {
		t.Errorf("header missing borderFills")
	}
	section := hwpxEntry(t, b, "Contents/section0.xml")
	for _, want := range []string{
		`rowCnt="2" colCnt="3"`, `colSpan="2" rowSpan="1"`, `colSpan="1" rowSpan="2"`,
		`colAddr="2" rowAddr="0"`, `<hp:subList`, `머리`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("section missing %s", want)
		}
	}
	txt, err := HwpxText(b)
	if err != nil {
		t.Fatalf("HwpxText: %v", err)
	}
	for _, want := range []string{"머리", "세로", "한", "둘"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q", want)
		}
	}
}

func TestHwpxTableWriteParseRoundTrip(t *testing.T) {
	orig := &DocModel{Blocks: []Block{
		&Para{Runs: []Run{{Text: "표 앞"}}},
		&Table{Rows: [][]Cell{
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "굵게", Bold: true}}}}, ColSpan: 2},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "세로칸"}}}}, RowSpan: 2}},
			{{Blocks: []Block{&Para{Runs: []Run{{Text: "한"}}}}},
				{Blocks: []Block{&Para{Runs: []Run{{Text: "둘"}}}}}},
		}},
		&Para{Runs: []Run{{Text: "표 뒤"}}},
	}}
	parsed, err := parseHwpx(writeHwpx(orig))
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	assertDocEqual(t, parsed, orig)
}

func TestHwpxImageWriteParseRoundTrip(t *testing.T) {
	img := &Image{MIME: "image/png", Data: tinyPNG(t, 20, 10), WPt: 120, HPt: 60}
	orig := &DocModel{Blocks: []Block{
		&Para{Runs: []Run{{Text: "앞 문단"}}},
		img,
		&Para{Runs: []Run{{Text: "뒤 문단"}}},
	}}
	parsed, err := parseHwpx(writeHwpx(orig))
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	assertDocEqual(t, parsed, orig)
}

// makeTestHwpx zips sectionXML into a minimal hwpx package (Contents/section0.xml
// only — parseHwpx does not require header.xml or the mimetype entry to be present).
func makeTestHwpx(t *testing.T, sectionXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("Contents/section0.xml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte(sectionXML)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestParseHwpxMalformedNestedTcNoPanic(t *testing.T) {
	sectionXML := `<hp:sec xmlns:hp="http://www.hancom.co.kr/hwpml/2011/paragraph">` +
		`<hp:p><hp:run><hp:tbl><hp:tr><hp:tc><hp:tc>` +
		`<hp:subList><hp:p><hp:run><hp:t>x</hp:t></hp:run></hp:p></hp:subList>` +
		`</hp:tc></hp:tc></hp:tr></hp:tbl></hp:run></hp:p>` +
		`</hp:sec>`
	if _, err := parseHwpx(makeTestHwpx(t, sectionXML)); err != nil {
		t.Logf("clean error is acceptable: %v", err)
	}
}

func TestParseHwpxHugeSpanClamped(t *testing.T) {
	sectionXML := `<hp:sec xmlns:hp="http://www.hancom.co.kr/hwpml/2011/paragraph">` +
		`<hp:p><hp:run><hp:tbl><hp:tr><hp:tc>` +
		`<hp:subList><hp:p><hp:run><hp:t>x</hp:t></hp:run></hp:p></hp:subList>` +
		`<hp:cellSpan colSpan="1000000000" rowSpan="1"/>` +
		`</hp:tc></hp:tr></hp:tbl></hp:run></hp:p>` +
		`</hp:sec>`
	d, err := parseHwpx(makeTestHwpx(t, sectionXML))
	if err != nil {
		t.Fatalf("parseHwpx: %v", err)
	}
	if c := d.Blocks[0].(*Table).Rows[0][0]; c.ColSpan > maxTableSpan {
		t.Fatalf("span not clamped: %d", c.ColSpan)
	}
	// the full conversion must stay small
	out, err := HwpxToDocx(makeTestHwpx(t, sectionXML))
	if err != nil {
		t.Fatalf("HwpxToDocx: %v", err)
	}
	if len(out) > 1<<20 {
		t.Fatalf("amplified output: %d bytes", len(out))
	}
}

func TestParseHwpxSkipsCorruptBinData(t *testing.T) {
	src := writeHwpx(&DocModel{Blocks: []Block{&Para{Runs: []Run{{Text: "본문"}}}}})
	// re-zip with an extra corrupt-deflate BinData entry
	zr, _ := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range zr.File {
		hdr := f.FileHeader
		dst, _ := w.CreateRaw(&hdr)
		rc, _ := f.OpenRaw()
		io.Copy(dst, rc)
	}
	corrupt := &zip.FileHeader{Name: "BinData/broken.png", Method: zip.Deflate}
	corrupt.CompressedSize64 = 4
	corrupt.UncompressedSize64 = 100
	corrupt.CRC32 = 0xdeadbeef
	cw, _ := w.CreateRaw(corrupt)
	cw.Write([]byte{0xde, 0xad, 0xbe, 0xef})
	w.Close()
	d, err := parseHwpx(buf.Bytes())
	if err != nil {
		t.Fatalf("corrupt BinData must not abort the parse: %v", err)
	}
	if len(d.Blocks) == 0 {
		t.Fatal("document content lost")
	}
}
