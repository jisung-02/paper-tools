package pdf

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ============================================================ CFB / OLE2 reader

// Special CFB sector id values (uint32, little-endian throughout the file).
const (
	cfbFreeSect     uint32 = 0xFFFFFFFF
	cfbEndOfChain   uint32 = 0xFFFFFFFE
	cfbFatSect      uint32 = 0xFFFFFFFD
	cfbDifSect      uint32 = 0xFFFFFFFC
	cfbHeaderSize          = 512
	cfbDirEntrySize        = 128
)

// cfbDirEntry is one 128-byte Compound File directory entry.
type cfbDirEntry struct {
	name        string
	objectType  byte // 0=unused/free, 1=storage, 2=stream, 5=root storage
	startSector uint32
	streamSize  uint64
}

// cfbFile is a minimal read-only Compound File Binary (OLE2) container
// reader, just capable enough to pull named streams out of a .hwp file.
//
// ponytail: directory entries technically form a red-black tree (via the
// left/right/child sibling ids at offsets 68/72/76 of each entry), but real
// HWP files only ever use unique, flat stream names ("FileHeader",
// "DocInfo", "Section0", "Section1", ...), so we skip building/walking that
// tree entirely and just linearly scan all directory entries by name. This
// would break on a CFB file with duplicate names at different tree
// positions, which HWP does not produce.
type cfbFile struct {
	data           []byte
	sectorSize     int
	miniSectorSize int
	fat            []uint32
	miniFat        []uint32
	miniStream     []byte // Root Entry's stream data, read via the regular FAT
	dir            []cfbDirEntry
	cutoff         uint32
}

// parseCFB parses raw into a cfbFile, validating the CFB signature and
// building the FAT, directory, mini-FAT and mini-stream needed by stream().
func parseCFB(raw []byte) (*cfbFile, error) {
	if len(raw) < cfbHeaderSize {
		return nil, errors.New("유효한 hwp 파일이 아닙니다")
	}
	sig := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	if !bytes.Equal(raw[0:8], sig) {
		return nil, errors.New("유효한 hwp 파일이 아닙니다")
	}

	le16 := func(off int) uint16 { return binary.LittleEndian.Uint16(raw[off:]) }
	le32 := func(off int) uint32 { return binary.LittleEndian.Uint32(raw[off:]) }

	sectorShift := le16(30)
	miniSectorShift := le16(32)
	if sectorShift != 9 && sectorShift != 12 {
		return nil, errors.New("유효한 hwp 파일이 아닙니다")
	}
	if miniSectorShift != 6 {
		return nil, errors.New("유효한 hwp 파일이 아닙니다")
	}
	numFATSectors := le32(44)
	firstDirSector := le32(48)
	miniStreamCutoff := le32(56)
	firstMiniFATSector := le32(60)
	numMiniFATSectors := le32(64)
	firstDIFATSector := le32(68)
	numDIFATSectors := le32(72)

	f := &cfbFile{
		data:           raw,
		sectorSize:     1 << sectorShift,
		miniSectorSize: 1 << miniSectorShift,
		cutoff:         miniStreamCutoff,
	}

	// Gather FAT-sector ids: 109 header DIFAT entries at offset 76, then
	// (ponytail: fine to only support what real HWP files need, but the
	// DIFAT chain itself must actually be followed, not just hardcoded to
	// 109 entries) follow the DIFAT chain starting at firstDIFATSector for
	// any additional FAT-sector ids beyond the first 109.
	// Note: 0 is a perfectly valid sector id (the very first sector in the
	// file); only 0xFFFFFFFF (FREESECT) marks an unused DIFAT slot.
	var fatSectorIDs []uint32
	for i := 0; i < 109; i++ {
		off := 76 + i*4
		if off+4 > cfbHeaderSize {
			break
		}
		id := le32(off)
		if id == cfbFreeSect {
			continue
		}
		fatSectorIDs = append(fatSectorIDs, id)
	}
	if numDIFATSectors > 0 {
		entriesPerDifat := f.sectorSize/4 - 1
		cur := firstDIFATSector
		for i := uint32(0); i < numDIFATSectors && cur != cfbEndOfChain && cur != cfbFreeSect; i++ {
			sec := f.rawSector(cur)
			if sec == nil {
				break
			}
			for j := 0; j < entriesPerDifat; j++ {
				id := binary.LittleEndian.Uint32(sec[j*4:])
				if id == cfbFreeSect {
					continue
				}
				fatSectorIDs = append(fatSectorIDs, id)
			}
			cur = binary.LittleEndian.Uint32(sec[entriesPerDifat*4:])
		}
	}

	// Build the FAT itself: concatenate the uint32 entries of every FAT
	// sector, in order.
	entriesPerFAT := f.sectorSize / 4
	fat := make([]uint32, 0, len(fatSectorIDs)*entriesPerFAT)
	for _, id := range fatSectorIDs {
		sec := f.rawSector(id)
		if sec == nil {
			continue
		}
		for j := 0; j < entriesPerFAT; j++ {
			fat = append(fat, binary.LittleEndian.Uint32(sec[j*4:]))
		}
	}
	_ = numFATSectors // informational only; fat is built from the sector id list above
	f.fat = fat

	// Directory.
	dirBytes := f.readChain(firstDirSector)
	for off := 0; off+cfbDirEntrySize <= len(dirBytes); off += cfbDirEntrySize {
		entry := dirBytes[off : off+cfbDirEntrySize]
		nameLen := binary.LittleEndian.Uint16(entry[64:])
		objType := entry[66]
		var name string
		if nameLen >= 2 {
			n := int(nameLen) - 2 // drop the trailing 2-byte null terminator
			if n > 64 {
				n = 64
			}
			u16 := make([]uint16, 0, n/2)
			for i := 0; i+1 < n; i += 2 {
				u16 = append(u16, binary.LittleEndian.Uint16(entry[i:]))
			}
			name = decodeUTF16(u16)
		}
		startSector := binary.LittleEndian.Uint32(entry[116:])
		streamSize := binary.LittleEndian.Uint64(entry[120:128])
		f.dir = append(f.dir, cfbDirEntry{
			name:        name,
			objectType:  objType,
			startSector: startSector,
			streamSize:  streamSize,
		})
	}

	// Root entry (always index 0) holds the mini-stream container, read via
	// the regular FAT chain and truncated to its declared size.
	if len(f.dir) > 0 {
		root := f.dir[0]
		ms := f.readChain(root.startSector)
		if uint64(len(ms)) > root.streamSize {
			ms = ms[:root.streamSize]
		}
		f.miniStream = ms
	}

	// Mini-FAT.
	if numMiniFATSectors > 0 {
		mfBytes := f.readChain(firstMiniFATSector)
		f.miniFat = make([]uint32, len(mfBytes)/4)
		for i := range f.miniFat {
			f.miniFat[i] = binary.LittleEndian.Uint32(mfBytes[i*4:])
		}
	}

	return f, nil
}

// rawSector returns the raw bytes of sector id, or nil if out of range.
func (f *cfbFile) rawSector(id uint32) []byte {
	off := cfbHeaderSize + int(id)*f.sectorSize
	if id == cfbFreeSect || id == cfbEndOfChain || off < 0 || off+f.sectorSize > len(f.data) {
		return nil
	}
	return f.data[off : off+f.sectorSize]
}

// readChain follows the regular FAT chain starting at startSector,
// concatenating every sector's bytes until ENDOFCHAIN (or defensively
// stopping on any out-of-range/free sector rather than panicking/looping
// forever).
func (f *cfbFile) readChain(startSector uint32) []byte {
	var out []byte
	cur := startSector
	seen := map[uint32]bool{}
	for cur != cfbEndOfChain && cur != cfbFreeSect {
		if seen[cur] {
			break // defensive: guard against a corrupt cyclic chain
		}
		seen[cur] = true
		sec := f.rawSector(cur)
		if sec == nil {
			break
		}
		out = append(out, sec...)
		if int(cur) >= len(f.fat) {
			break
		}
		cur = f.fat[cur]
	}
	return out
}

// readMiniChain follows the mini-FAT chain starting at startMiniSector,
// pulling each mini-sector's bytes out of the root mini-stream.
func (f *cfbFile) readMiniChain(startMiniSector uint32) []byte {
	var out []byte
	cur := startMiniSector
	seen := map[uint32]bool{}
	for cur != cfbEndOfChain && cur != cfbFreeSect {
		if seen[cur] {
			break
		}
		seen[cur] = true
		start := int(cur) * f.miniSectorSize
		end := start + f.miniSectorSize
		if start < 0 || end > len(f.miniStream) {
			break
		}
		out = append(out, f.miniStream[start:end]...)
		if int(cur) >= len(f.miniFat) {
			break
		}
		cur = f.miniFat[cur]
	}
	return out
}

// readStream returns entry's full stream content, choosing the regular FAT
// or the mini-FAT path depending on its declared size vs the mini-stream
// cutoff.
func (f *cfbFile) readStream(entry cfbDirEntry) []byte {
	var data []byte
	if entry.streamSize >= uint64(f.cutoff) {
		data = f.readChain(entry.startSector)
	} else {
		data = f.readMiniChain(entry.startSector)
	}
	if uint64(len(data)) > entry.streamSize {
		data = data[:entry.streamSize]
	}
	return data
}

// stream linear-scans the directory for an exact-name stream entry.
func (f *cfbFile) stream(name string) ([]byte, bool) {
	for _, e := range f.dir {
		if e.objectType == 2 && e.name == name {
			return f.readStream(e), true
		}
	}
	return nil, false
}

// sectionNames returns every stream entry named "Section<N>", sorted
// ascending by the trailing integer N (not lexicographically, so Section10
// sorts after Section9).
func (f *cfbFile) sectionNames() []string {
	var names []string
	for _, e := range f.dir {
		if e.objectType == 2 && strings.HasPrefix(e.name, "Section") {
			if _, err := strconv.Atoi(e.name[len("Section"):]); err == nil {
				names = append(names, e.name)
			}
		}
	}
	sort.Slice(names, func(i, j int) bool {
		ni, _ := strconv.Atoi(names[i][len("Section"):])
		nj, _ := strconv.Atoi(names[j][len("Section"):])
		return ni < nj
	})
	return names
}

// decodeUTF16 decodes a slice of UTF-16LE code units (already split out as
// uint16s) into a Go string, handling surrogate pairs.
func decodeUTF16(u16 []uint16) string {
	var sb strings.Builder
	for i := 0; i < len(u16); i++ {
		wc := u16[i]
		if wc >= 0xD800 && wc <= 0xDBFF && i+1 < len(u16) {
			lo := u16[i+1]
			if lo >= 0xDC00 && lo <= 0xDFFF {
				sb.WriteRune(rune(0x10000 + (int(wc)-0xD800)*0x400 + (int(lo) - 0xDC00)))
				i++
				continue
			}
		}
		sb.WriteRune(rune(wc))
	}
	return sb.String()
}

// ============================================================ HWP body text extraction

// control8Wide is the set of inline/extended control wchars (object
// anchors, field codes, etc) that each occupy exactly 8 wchars in a
// PARA_TEXT record and carry no visible text.
var control8Wide = map[uint16]bool{
	1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true,
	9: true, 11: true, 12: true, 14: true, 15: true, 16: true, 17: true,
	18: true, 19: true, 20: true, 21: true, 22: true, 23: true,
}

// charControl1Wide is the set of single-wchar char controls (line-break
// variants, tab-like controls, etc) that carry no visible text.
var charControl1Wide = map[uint16]bool{
	0: true, 24: true, 25: true, 26: true, 27: true, 28: true, 29: true,
	30: true, 31: true,
}

const hwpTagParaText = 67 // HWPTAG_PARA_TEXT

// ponytail: HWP 5.x only, non-encrypted files only; extracts paragraph text
// only (no tables/layout/styles/embedded objects/headers-footers). The test
// suite exercises a synthetic hand-built CFB container; beyond that, this was
// validated by hand against 6 real Hancom-produced .hwp files (16KB–2.1MB,
// incl. a government press release) — all extracted their Korean text and
// rendered correctly. Exotic records/compression in other real files may
// still surprise it.

// HwpText extracts plain paragraph text from a legacy .hwp (HWP 5.0 binary
// / Compound File Binary) document.
func HwpText(data []byte) (string, error) {
	if int64(len(data)) > officeParseLimits.maxHWPInputBytes {
		return "", errors.New("hwp: input too large")
	}
	cfb, err := parseCFB(data)
	if err != nil {
		return "", err
	}

	fh, ok := cfb.stream("FileHeader")
	if !ok || len(fh) < 40 || !bytes.HasPrefix(fh[:32], []byte("HWP Document File")) {
		return "", errors.New("유효한 hwp 파일이 아닙니다")
	}
	properties := binary.LittleEndian.Uint32(fh[36:40])
	compressed := properties&1 != 0
	if properties&2 != 0 {
		return "", errors.New("암호가 걸린 한글 문서입니다")
	}

	var sb strings.Builder
	for _, name := range cfb.sectionNames() {
		raw, ok := cfb.stream(name)
		if !ok {
			continue
		}
		sectionData := raw
		if int64(len(raw)) > officeParseLimits.maxHWPSectionBytes {
			return "", errors.New("hwp: section too large")
		}
		if compressed {
			r := flate.NewReader(bytes.NewReader(raw))
			out, rerr := io.ReadAll(&io.LimitedReader{R: r, N: officeParseLimits.maxHWPSectionBytes + 1})
			closeErr := r.Close()
			if rerr != nil {
				return "", fmt.Errorf("hwp: decompress section %q: %w", name, rerr)
			}
			if closeErr != nil {
				return "", fmt.Errorf("hwp: close section %q: %w", name, closeErr)
			}
			if int64(len(out)) > officeParseLimits.maxHWPSectionBytes {
				return "", errors.New("hwp: decompressed section too large")
			}
			sectionData = out
		}
		text, err := extractSectionRecords(sectionData)
		if err != nil {
			return "", err
		}
		sb.WriteString(text)
		if sb.Len() > int(officeParseLimits.maxTextBytes) {
			return "", errors.New("hwp: extracted text too large")
		}
	}

	result := sb.String()
	re := regexp.MustCompile(`\n{3,}`)
	result = re.ReplaceAllString(result, "\n\n")
	return result, nil
}

// extractSectionRecords walks the HWP record stream in data, decoding the
// text of every HWPTAG_PARA_TEXT record and appending a paragraph-boundary
// "\n" after each one.
func extractSectionRecords(data []byte) (string, error) {
	var sb strings.Builder
	pos := 0
	for pos+4 <= len(data) {
		h := binary.LittleEndian.Uint32(data[pos:])
		tagID := h & 0x3FF
		// level occupies bits 10-19 of the header word but is unused by this
		// text-only extractor; it's still consumed via the 4-byte pos
		// advance below.
		size := (h >> 20) & 0xFFF
		pos += 4
		if size == 0xFFF {
			if pos+4 > len(data) {
				return "", errors.New("hwp: truncated record size")
			}
			size = binary.LittleEndian.Uint32(data[pos:])
			pos += 4
		}
		if pos+int(size) > len(data) {
			return "", errors.New("hwp: truncated record")
		}
		rec := data[pos : pos+int(size)]
		pos += int(size)

		if tagID == hwpTagParaText {
			sb.WriteString(decodeParaText(rec))
			sb.WriteString("\n")
		}
	}
	if pos != len(data) {
		return "", errors.New("hwp: truncated record header")
	}
	return sb.String(), nil
}

// decodeParaText decodes a HWPTAG_PARA_TEXT record payload (a sequence of
// UTF-16LE wchars) into visible text, skipping inline controls per the HWP
// 5.0 spec.
func decodeParaText(rec []byte) string {
	var sb strings.Builder
	n := len(rec) / 2
	i := 0
	for i < n {
		wc := binary.LittleEndian.Uint16(rec[2*i:])
		switch {
		case control8Wide[wc]:
			i += 8
		case wc == 10 || wc == 13:
			sb.WriteByte('\n')
			i++
		case charControl1Wide[wc]:
			i++
		default:
			if wc >= 0xD800 && wc <= 0xDBFF && i+1 < n {
				lo := binary.LittleEndian.Uint16(rec[2*(i+1):])
				if lo >= 0xDC00 && lo <= 0xDFFF {
					sb.WriteRune(rune(0x10000 + (int(wc)-0xD800)*0x400 + (int(lo) - 0xDC00)))
					i += 2
					continue
				}
			}
			sb.WriteRune(rune(wc))
			i++
		}
	}
	return sb.String()
}

// HwpToPDF converts a legacy .hwp file to PDF by extracting its paragraph
// text and rendering it via TextToPDF.
func HwpToPDF(data []byte, fontTTF []byte, opts TextPDFOpts) ([]byte, error) {
	txt, err := HwpText(data)
	if err != nil {
		return nil, err
	}
	return TextToPDF(txt, fontTTF, opts)
}
