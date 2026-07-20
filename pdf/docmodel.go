package pdf

import "strings"

// Hostile-input ceilings. Converters run client-side in the user's own tab,
// so these only prevent self-DoS (a crashed tab), not a cross-user boundary;
// they bound worst-case memory amplification from adversarial files
// (millions of empty paragraphs, a distinct style per run, ...).
const (
	maxModelBlocks = 100000 // Para+Table blocks, including inside table cells
	maxModelRuns   = 250000 // runs across the document
	maxHwpxCharPrs = 4096   // distinct charPr entries writeHwpx will emit
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
	out := &DocModel{}
	for _, blk := range doc.Blocks {
		p, ok := blk.(*Para)
		if !ok {
			out.Blocks = append(out.Blocks, blk)
			continue
		}
		cur := &Para{Align: p.Align, Heading: p.Heading}
		for _, r := range p.Runs {
			segs := strings.Split(r.Text, "\n")
			for i, seg := range segs {
				if i > 0 {
					cur.Runs = mergeRuns(cur.Runs)
					out.Blocks = append(out.Blocks, cur)
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
		out.Blocks = append(out.Blocks, cur)
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
