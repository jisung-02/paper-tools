package pdf

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// Hostile-input ceilings. Converters run client-side in the user's own tab,
// so these only prevent self-DoS (a crashed tab), not a cross-user boundary;
// they bound worst-case memory amplification from adversarial files
// (millions of empty paragraphs, a distinct style per run, ...).
const (
	maxModelBlocks = 100000 // Para+Table blocks, including inside table cells
	maxModelRuns   = 250000 // runs across the document
	maxHwpxCharPrs = 4096   // distinct charPr entries writeHwpx will emit
	maxTableSpan   = 1000   // ColSpan/RowSpan ceiling; hostile span values otherwise drive writers' per-column loops to GB-scale output
	maxModelImages = 200    // Image blocks across the document
)

// DocModel is the shared intermediate document model for office conversions:
// readers (docx/hwpx) produce it, writers (docx/hwpx/PDF) consume it.
type DocModel struct{ Blocks []Block }

// Block is one document body element.
// ponytail: stage 1 has paragraphs only; tables/images are later stages.
type Block interface{ isBlock() }

// Align is a paragraph horizontal alignment. AlignDefault means "the
// format's own default" (docx: left, hwpx: justify) and emits no explicit
// alignment on write.
type Align int

const (
	AlignDefault Align = iota
	AlignLeft
	AlignCenter
	AlignRight
)

// Para is one paragraph: block-level formatting plus its formatted runs.
// Heading is 0 for body text, 1..6 for heading levels.
type Para struct {
	Align   Align
	Heading int
	Runs    []Run
}

func (*Para) isBlock() {}

// Table is a rectangular grid of cells. Rows hold only real cells — grid
// positions covered by a ColSpan/RowSpan from another cell have no
// placeholder entry.
type Table struct{ Rows [][]Cell }

func (*Table) isBlock() {}

// Cell is one table cell: nested block content plus its span. Spans of 0
// are treated as 1 via colSpan()/rowSpan().
type Cell struct {
	Blocks  []Block
	ColSpan int
	RowSpan int
}

func (c Cell) colSpan() int {
	if c.ColSpan < 1 {
		return 1
	}
	return c.ColSpan
}

func (c Cell) rowSpan() int {
	if c.RowSpan < 1 {
		return 1
	}
	return c.RowSpan
}

// Image is a block-level embedded picture (PNG or JPEG bytes). WPt/HPt are
// the display size in points; 0 means "derive from pixel size at 96 DPI".
// ponytail: images are standalone blocks — inline anchoring/wrap positions
// are not modeled.
type Image struct {
	MIME     string
	Data     []byte
	WPt, HPt float64
}

func (*Image) isBlock() {}

// imgKey identifies an image by its backing bytes (same slice = same image),
// so re-serializing a parsed document doesn't duplicate shared media.
// ponytail: distinct-but-equal byte slices still duplicate; only shared
// backings (the parsers' cache guarantees) dedup.
type imgKey struct {
	p *byte
	n int
}

func imageKey(data []byte) imgKey {
	if len(data) == 0 {
		return imgKey{}
	}
	return imgKey{&data[0], len(data)}
}

// sniffImageMIME identifies the two supported formats by magic bytes.
func sniffImageMIME(data []byte) string {
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	return ""
}

// imagePixelSize reads pixel dimensions from the PNG IHDR or JPEG SOF
// header without decoding the raster.
func imagePixelSize(data []byte) (int, int, bool) {
	switch sniffImageMIME(data) {
	case "image/png":
		if len(data) >= 24 {
			w := int(binary.BigEndian.Uint32(data[16:20]))
			h := int(binary.BigEndian.Uint32(data[20:24]))
			if w > 0 && h > 0 {
				return w, h, true
			}
		}
	case "image/jpeg":
		if sof, err := scanJPEGHeader(data); err == nil {
			return sof.width, sof.height, true
		}
	}
	return 0, 0, false
}

// displaySizePt resolves the display size in points: explicit WPt/HPt when
// both set, else pixel size at 96 DPI, else a 200pt square fallback.
func (im *Image) displaySizePt() (float64, float64) {
	if im.WPt > 0 && im.HPt > 0 {
		return im.WPt, im.HPt
	}
	if w, h, ok := imagePixelSize(im.Data); ok {
		return float64(w) * 72 / 96, float64(h) * 72 / 96
	}
	return 200, 200
}

// Run is a maximal span of identically-formatted text within a paragraph.
// SizePt 0 means "inherit the default body size"; Color is 0xRRGGBB.
type Run struct {
	Text                            string
	Bold, Italic, Underline, Strike bool
	SizePt                          float64
	Color                           uint32
}

// style returns r with Text cleared so two runs' formatting can be compared
// with ==.
func (r Run) style() Run { r.Text = ""; return r }

// mergeRuns drops empty runs and collapses adjacent runs with identical
// formatting into one.
func mergeRuns(runs []Run) []Run {
	var out []Run
	for _, r := range runs {
		if r.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].style() == r.style() {
			out[n-1].Text += r.Text
			continue
		}
		out = append(out, r)
	}
	return out
}

// normalizeDoc returns a normalized shallow copy of doc (paragraphs are
// rebuilt; other block kinds pass through by reference) where any "\n"
// inside run text splits the paragraph (preserving its Align/Heading) and
// adjacent same-style runs are merged. Writers call this first so they
// never see embedded newlines.
// ponytail: in-paragraph line breaks become separate paragraphs (slight
// spacing change) instead of format-specific break elements.
func normalizeDoc(doc *DocModel) *DocModel {
	return &DocModel{Blocks: normalizeBlocks(doc.Blocks)}
}

// normalizeBlocks applies normalizeDoc's paragraph-splitting rules to a
// block list, recursing into table cells.
func normalizeBlocks(blocks []Block) []Block {
	var out []Block
	for _, blk := range blocks {
		switch b := blk.(type) {
		case *Para:
			p := b
			cur := &Para{Align: p.Align, Heading: p.Heading}
			for _, r := range p.Runs {
				segs := strings.Split(r.Text, "\n")
				for i, seg := range segs {
					if i > 0 {
						cur.Runs = mergeRuns(cur.Runs)
						out = append(out, cur)
						cur = &Para{Align: p.Align, Heading: p.Heading}
					}
					if seg != "" {
						nr := r
						nr.Text = seg
						cur.Runs = append(cur.Runs, nr)
					}
				}
			}
			cur.Runs = mergeRuns(cur.Runs)
			out = append(out, cur)
		case *Table:
			nt := &Table{}
			for _, row := range b.Rows {
				nr := make([]Cell, len(row))
				for i, c := range row {
					nr[i] = Cell{Blocks: normalizeBlocks(c.Blocks), ColSpan: c.colSpan(), RowSpan: c.rowSpan()}
				}
				nt.Rows = append(nt.Rows, nr)
			}
			out = append(out, nt)
		default:
			out = append(out, blk)
		}
	}
	return out
}

// docFromParas builds a plain unformatted DocModel, one paragraph per element
// (used by the text-only PDF→office path).
func docFromParas(paras []string) *DocModel {
	d := &DocModel{}
	for _, p := range paras {
		para := &Para{}
		if p != "" {
			para.Runs = []Run{{Text: p}}
		}
		d.Blocks = append(d.Blocks, para)
	}
	return d
}

// headingSizePt is the default visual size for a heading level, derived
// from the 11pt body default and md.go's headingScale.
func headingSizePt(level int) float64 { return 11 * headingScale(level) }

// gridItem is one grid placement reported by tableGrid: an explicit cell
// (Cell != nil) or a position covered by a RowSpan from an earlier row
// (Cell == nil; docx emits a vMerge-continue cell there). W is the width
// in grid columns either way.
type gridItem struct {
	Row, Col, W int
	Cell        *Cell
}

// tableGrid simulates grid occupancy and returns the grid width plus every
// placement in row-major, column order. Rows narrower than the grid are
// reported as-is (no padding); irregular tables degrade gracefully.
func tableGrid(t *Table) (int, []gridItem) {
	cols := 0
	var items []gridItem
	type span struct{ w, rows, born int }
	active := map[int]*span{} // start col -> live rowspan
	for r := range t.Rows {
		c := 0
		emitCovered := func() {
			for {
				sp, ok := active[c]
				if !ok || sp.born == r {
					return
				}
				items = append(items, gridItem{Row: r, Col: c, W: sp.w})
				c += sp.w
			}
		}
		emitCovered()
		for i := range t.Rows[r] {
			cell := &t.Rows[r][i]
			w := cell.colSpan()
			items = append(items, gridItem{Row: r, Col: c, W: w, Cell: cell})
			if rs := cell.rowSpan(); rs > 1 {
				active[c] = &span{w: w, rows: rs - 1, born: r}
			}
			c += w
			emitCovered()
		}
		if c > cols {
			cols = c
		}
		for k, sp := range active {
			if sp.born == r {
				continue
			}
			sp.rows--
			if sp.rows == 0 {
				delete(active, k)
			}
		}
	}
	return cols, items
}
