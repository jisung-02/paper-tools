package pdf

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// buildSyntheticHWP hand-assembles a minimal but genuinely FAT-chain-valid
// Compound File Binary (OLE2) container holding a synthetic HWP 5.0
// document: a FileHeader stream (compressed bit set), a DocInfo stream, and
// a BodyText storage containing one Section0 stream with a single
// raw-deflate-compressed HWPTAG_PARA_TEXT record.
//
// ponytail: every stream here is small enough to live entirely in the
// mini-stream (below the 4096-byte cutoff), which is the common real-world
// case for a tiny HWP document and exercises the mini-FAT code path. The
// container is simplified relative to a fully general CFB writer: only one
// FAT sector (numFATSectors=1, well under the 128 entries one FAT sector
// can hold), no DIFAT chain sectors (numDIFATSectors=0, all sector ids fit
// in the header's 109 inline DIFAT entries), no free/unused sectors, and no
// actual nested storage tree (the "BodyText" entry is a flat directory
// entry alongside its "child", not a real parent/child link, since hwp.go's
// reader deliberately skips the red-black directory tree and matches
// streams by name — see the comment on cfbFile). Every sector reference
// below is nonetheless a real, correctly-chained FAT/mini-FAT lookup that
// is exercised through hwp.go's own cfbFile reader; nothing is
// special-cased for the test.
func buildSyntheticHWP(t testing.TB) []byte {
	t.Helper()
	const sectorSize = 512
	const miniSectorSize = 64

	// ---- FileHeader stream (256 bytes) ----
	fileHeader := make([]byte, 256)
	copy(fileHeader[:32], []byte("HWP Document File"))
	binary.LittleEndian.PutUint32(fileHeader[32:36], 0x05000000)
	binary.LittleEndian.PutUint32(fileHeader[36:40], 1) // properties: bit0 compressed=1, bit1 password=0

	// ---- DocInfo stream (arbitrary content; just needs to exist) ----
	docInfo := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x01, 0x02}

	// ---- Section0: one HWPTAG_PARA_TEXT record for "안녕하세요 HWP 123",
	// with a CONTROL_8WIDE control (wc=4) plus 7 filler wchars spliced in
	// between the two text halves, to prove the skip-8-wchars logic
	// doesn't leak filler wchars into the decoded text. The filler value
	// 0xFFFF (a Unicode noncharacter) would be an obviously-wrong visible
	// character if it leaked through.
	text1 := utf16.Encode([]rune("안녕하세요"))
	text2 := utf16.Encode([]rune(" HWP 123"))
	var payload []uint16
	payload = append(payload, text1...)
	payload = append(payload, 4) // CONTROL_8WIDE control code
	for i := 0; i < 7; i++ {
		payload = append(payload, 0xFFFF) // filler; must never appear in output
	}
	payload = append(payload, text2...)

	payloadBytes := make([]byte, len(payload)*2)
	for i, wc := range payload {
		binary.LittleEndian.PutUint16(payloadBytes[2*i:], wc)
	}

	headerWord := (uint32(len(payloadBytes)) << 20) | (0 << 10) | uint32(hwpTagParaText)
	recHeader := make([]byte, 4)
	binary.LittleEndian.PutUint32(recHeader, headerWord)
	record := append(recHeader, payloadBytes...)

	var compBuf bytes.Buffer
	fw, err := flate.NewWriter(&compBuf, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate.NewWriter: %v", err)
	}
	if _, err := fw.Write(record); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}
	section0 := compBuf.Bytes()

	// ---- Assign mini sectors (64 bytes each) for each stream. ----
	type miniStream struct {
		name       string
		data       []byte
		startMini  uint32
		numSectors int
	}
	ceilDiv := func(a, b int) int { return (a + b - 1) / b }

	streams := []*miniStream{
		{name: "FileHeader", data: fileHeader},
		{name: "DocInfo", data: docInfo},
		{name: "Section0", data: section0},
	}

	var miniStreamBytes []byte
	var miniFatEntries []uint32
	cursor := uint32(0)
	for _, s := range streams {
		s.numSectors = ceilDiv(len(s.data), miniSectorSize)
		if s.numSectors == 0 {
			s.numSectors = 1
		}
		s.startMini = cursor
		padded := make([]byte, s.numSectors*miniSectorSize)
		copy(padded, s.data)
		miniStreamBytes = append(miniStreamBytes, padded...)
		for i := 0; i < s.numSectors; i++ {
			if i == s.numSectors-1 {
				miniFatEntries = append(miniFatEntries, cfbEndOfChain)
			} else {
				miniFatEntries = append(miniFatEntries, cursor+1)
			}
			cursor++
		}
	}

	if len(miniStreamBytes) > sectorSize {
		t.Fatalf("test setup: mini-stream content %d bytes exceeds the single regular sector this test allocates for it", len(miniStreamBytes))
	}
	if len(miniFatEntries)*4 > sectorSize {
		t.Fatalf("test setup: mini-FAT content exceeds the single regular sector this test allocates for it")
	}

	// ---- Regular sector layout: 0=FAT, 1-2=directory, 3=mini-stream container, 4=mini-FAT ----
	const (
		secFAT      = 0
		secDir1     = 1
		secDir2     = 2
		secMiniData = 3
		secMiniFAT  = 4
	)

	miniStreamSector := make([]byte, sectorSize)
	copy(miniStreamSector, miniStreamBytes)

	miniFatSector := make([]byte, sectorSize)
	for i := 0; i*4+4 <= sectorSize; i++ {
		if i < len(miniFatEntries) {
			binary.LittleEndian.PutUint32(miniFatSector[i*4:], miniFatEntries[i])
		} else {
			binary.LittleEndian.PutUint32(miniFatSector[i*4:], cfbFreeSect)
		}
	}

	findStream := func(name string) *miniStream {
		for _, s := range streams {
			if s.name == name {
				return s
			}
		}
		t.Fatalf("stream %s not found", name)
		return nil
	}
	fh := findStream("FileHeader")
	di := findStream("DocInfo")
	s0 := findStream("Section0")

	// ---- Directory entries ----
	type dirEnt struct {
		name        string
		objType     byte
		startSector uint32
		size        uint64
	}
	ents := []dirEnt{
		{"Root Entry", 5, secMiniData, uint64(len(miniStreamBytes))},
		{"FileHeader", 2, fh.startMini, uint64(len(fh.data))},
		{"DocInfo", 2, di.startMini, uint64(len(di.data))},
		{"BodyText", 1, 0, 0},
		{"Section0", 2, s0.startMini, uint64(len(s0.data))},
	}

	writeDirEntry := func(buf []byte, e dirEnt) {
		u16 := utf16.Encode([]rune(e.name))
		for i, u := range u16 {
			binary.LittleEndian.PutUint16(buf[i*2:], u)
		}
		nameLen := uint16(len(u16)*2 + 2) // includes trailing null terminator
		binary.LittleEndian.PutUint16(buf[64:], nameLen)
		buf[66] = e.objType
		binary.LittleEndian.PutUint32(buf[116:], e.startSector)
		binary.LittleEndian.PutUint64(buf[120:], e.size)
	}

	dirBytes := make([]byte, 2*sectorSize) // 2 dir sectors x 4 entries of 128 bytes each
	for i, e := range ents {
		writeDirEntry(dirBytes[i*cfbDirEntrySize:(i+1)*cfbDirEntrySize], e)
	}
	// Remaining slots (indices 5-7) are left all-zero -> nameLen 0 -> unused.

	// ---- FAT (single sector, 128 uint32 entries) ----
	fat := make([]uint32, sectorSize/4)
	for i := range fat {
		fat[i] = cfbFreeSect
	}
	fat[secFAT] = cfbFatSect
	fat[secDir1] = secDir2
	fat[secDir2] = cfbEndOfChain
	fat[secMiniData] = cfbEndOfChain
	fat[secMiniFAT] = cfbEndOfChain
	fatSector := make([]byte, sectorSize)
	for i, e := range fat {
		binary.LittleEndian.PutUint32(fatSector[i*4:], e)
	}

	// ---- Header (512 bytes) ----
	header := make([]byte, cfbHeaderSize)
	copy(header[0:8], []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	binary.LittleEndian.PutUint16(header[30:], 9) // sectorShift -> sectorSize 512
	binary.LittleEndian.PutUint16(header[32:], 6) // miniSectorShift -> miniSectorSize 64
	binary.LittleEndian.PutUint32(header[44:], 1) // numFATSectors
	binary.LittleEndian.PutUint32(header[48:], secDir1)
	binary.LittleEndian.PutUint32(header[56:], 4096) // miniStreamCutoffSize
	binary.LittleEndian.PutUint32(header[60:], secMiniFAT)
	binary.LittleEndian.PutUint32(header[64:], 1) // numMiniFATSectors
	binary.LittleEndian.PutUint32(header[68:], cfbEndOfChain)
	binary.LittleEndian.PutUint32(header[72:], 0) // numDIFATSectors
	// 109 DIFAT entries at offset 76: first is the (only) FAT sector id,
	// the rest are unused (FREESECT).
	binary.LittleEndian.PutUint32(header[76:], secFAT)
	for i := 1; i < 109; i++ {
		binary.LittleEndian.PutUint32(header[76+i*4:], cfbFreeSect)
	}

	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(fatSector)
	buf.Write(dirBytes)
	buf.Write(miniStreamSector)
	buf.Write(miniFatSector)
	return buf.Bytes()
}

func TestHwpText(t *testing.T) {
	data := buildSyntheticHWP(t)

	txt, err := HwpText(data)
	if err != nil {
		t.Fatalf("HwpText: %v", err)
	}
	if !strings.Contains(txt, "안녕하세요") {
		t.Errorf("extracted text missing \"안녕하세요\"; got: %q", txt)
	}
	if !strings.Contains(txt, "HWP 123") {
		t.Errorf("extracted text missing \"HWP 123\"; got: %q", txt)
	}
	if strings.ContainsRune(txt, 0xFFFF) {
		t.Errorf("extracted text leaked CONTROL_8WIDE filler wchars; got: %q", txt)
	}
	if want := "안녕하세요 HWP 123\n"; txt != want {
		t.Errorf("HwpText = %q, want %q", txt, want)
	}
}

func TestHwpToPDF(t *testing.T) {
	data := buildSyntheticHWP(t)
	font, err := os.ReadFile(filepath.Join("..", "web", "NanumGothic-Regular.ttf"))
	if err != nil {
		t.Fatalf("read font: %v", err)
	}

	pdfBytes, err := HwpToPDF(data, font, TextPDFOpts{})
	if err != nil {
		t.Fatalf("HwpToPDF: %v", err)
	}

	txt, err := ExtractText(pdfBytes)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(txt, "안녕하세요") {
		t.Errorf("round-tripped PDF text missing \"안녕하세요\"; got: %q", txt)
	}
}

func TestHwpTextInvalidSignature(t *testing.T) {
	bad := make([]byte, 512) // all zeros: not a CFB signature
	if _, err := HwpText(bad); err == nil {
		t.Fatalf("expected error for non-CFB input")
	}
}

// FuzzHwpToPDF exercises HWP (CFB container) parsing/conversion with
// arbitrary bytes; the only failure mode under test is a panic (errors are
// expected and ignored). The font is fixed to the app's real bundled font,
// matching wasm/hwp2pdf, which only lets the file bytes vary.
func FuzzHwpToPDF(f *testing.F) {
	font := testFont(f)
	f.Add([]byte(""))
	f.Add(make([]byte, 512)) // all zeros: not a CFB signature
	f.Add(buildSyntheticHWP(f))
	f.Fuzz(func(t *testing.T, data []byte) {
		HwpToPDF(data, font, TextPDFOpts{})
	})
}
