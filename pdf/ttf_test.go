package pdf

import (
	"os"
	"testing"
)

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
