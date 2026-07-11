package pdf

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestParseCmapFormat12RejectsOversizedUnicodeRange(t *testing.T) {
	data := make([]byte, 28)
	binary.BigEndian.PutUint32(data[12:16], 1)
	binary.BigEndian.PutUint32(data[16:20], 0)
	binary.BigEndian.PutUint32(data[20:24], 1_300_000)
	binary.BigEndian.PutUint32(data[24:28], 1)
	if got := parseCmapFormat12(data, 0); got != nil {
		t.Fatalf("oversized cmap produced %d mappings", len(got))
	}
}

func TestEmbedToUnicodeRejectsAmbiguousGlyphMapping(t *testing.T) {
	font := &ttfFont{runeToGID: map[rune]uint16{
		'A': 42,
		'B': 42,
	}}
	if _, err := embedToUnicode(&builder{}, font, []rune{'A', 'B'}); err == nil {
		t.Fatal("embedToUnicode accepted two Unicode runes for the same glyph ID")
	}
}

func TestTTFSubsetRoundTrip(t *testing.T) {
	data, err := os.ReadFile("../web/NanumGothic-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	f, err := parseTTF(data)
	if err != nil {
		t.Fatalf("parseTTF: %v", err)
	}
	runes := []rune("안녕Aa1")
	f.markUsed(runes...)
	sub := f.subset()
	t.Logf("subset size: %d bytes (original %d bytes)", len(sub), len(data))

	f2, err := parseTTF(sub)
	if err != nil {
		t.Fatalf("parseTTF(subset): %v", err)
	}
	if f2.numGlyphs != f.numGlyphs {
		t.Fatalf("numGlyphs mismatch: got %d want %d", f2.numGlyphs, f.numGlyphs)
	}
	for _, r := range runes {
		gid, ok := f.gid(r)
		if !ok {
			t.Fatalf("no gid for rune %q in original font", r)
		}
		lo, hi := f2.locaRange(gid)
		if !(hi > lo) {
			t.Errorf("rune %q (gid %d) has empty glyf in subset", r, gid)
		}
	}
}
