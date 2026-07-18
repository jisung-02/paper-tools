package imgconv

import (
	"image"
	"image/color"
	"image/draw"
	"sort"
)

// maxQuantizeSamples caps how many pixels medianCutQuantizer inspects when
// building a palette. Large images are stride-sampled down to roughly this
// many evenly spaced pixels instead of scanning every one.
const maxQuantizeSamples = 65536

// gifQuantizer is the shared Quantizer passed to gif.Encode by both Convert
// and Resize. It replaces the stdlib's default fixed Plan9 palette (image/
// color/palette) with one built from the actual image's colors, which is
// much closer for photographic content. Leaving gif.Options.Drawer nil
// keeps the stdlib's Floyd-Steinberg dithering against this palette.
var gifQuantizer draw.Quantizer = medianCutQuantizer{}

// medianCutQuantizer implements draw.Quantizer using median-cut: sample the
// image's pixels, then repeatedly split the color space along its longest
// axis (weighted by pixel count) until the requested number of colors is
// reached, taking the count-weighted average color of each resulting box.
//
// Transparency: GIF only supports 1-bit transparency. If any sampled pixel
// is more than half transparent (alpha < 128), palette slot 0 is reserved
// as fully transparent (alpha 0) and the remaining slots are built from the
// opaque pixels only.
type medianCutQuantizer struct{}

var _ draw.Quantizer = medianCutQuantizer{}

// colorCount is one distinct opaque RGB color seen while sampling, along
// with how many sampled pixels had that exact color.
type colorCount struct {
	r, g, b uint8
	count   int
}

func (q medianCutQuantizer) Quantize(p color.Palette, m image.Image) color.Palette {
	budget := cap(p) - len(p)
	if budget <= 0 {
		return p
	}

	colors, hasTransparency := sampleColors(m)

	opaqueBudget := budget
	if hasTransparency {
		if budget > 1 {
			opaqueBudget = budget - 1
		} else {
			opaqueBudget = 0
		}
	}

	palette := medianCutPalette(colors, opaqueBudget)

	if hasTransparency {
		p = append(p, color.NRGBA{R: 0, G: 0, B: 0, A: 0})
	}
	for _, c := range palette {
		if len(p) >= cap(p) {
			break
		}
		p = append(p, c)
	}
	return p
}

// sampleColors stride-samples up to maxQuantizeSamples pixels of m (evenly
// spaced across the image in row-major order for larger images) and
// returns a histogram of the distinct opaque colors seen, plus whether any
// sampled pixel was more than half transparent.
func sampleColors(m image.Image) ([]colorCount, bool) {
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, false
	}

	total := w * h
	step := 1
	if total > maxQuantizeSamples {
		step = (total + maxQuantizeSamples - 1) / maxQuantizeSamples
	}

	hist := make(map[uint32]int)
	hasTransparency := false

	flat := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if flat%step == 0 {
				c := color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
				if c.A < 128 {
					hasTransparency = true
				} else {
					key := uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
					hist[key]++
				}
			}
			flat++
		}
	}

	colors := make([]colorCount, 0, len(hist))
	for key, count := range hist {
		colors = append(colors, colorCount{
			r:     uint8(key >> 16),
			g:     uint8(key >> 8),
			b:     uint8(key),
			count: count,
		})
	}
	return colors, hasTransparency
}

// medianCutPalette reduces colors (a histogram of distinct opaque RGB
// colors) to at most budget representative colors via median-cut.
func medianCutPalette(colors []colorCount, budget int) []color.NRGBA {
	if budget < 1 || len(colors) == 0 {
		return nil
	}

	boxes := []colorBox{newColorBox(colors)}
	for len(boxes) < budget {
		splitIdx, bestScore := -1, -1
		for i := range boxes {
			if !boxes[i].splittable() {
				continue
			}
			if boxes[i].total > bestScore {
				bestScore = boxes[i].total
				splitIdx = i
			}
		}
		if splitIdx == -1 {
			break // no box can be usefully split any further
		}

		left, right := boxes[splitIdx].split()
		boxes[splitIdx] = left
		boxes = append(boxes, right)
	}

	out := make([]color.NRGBA, len(boxes))
	for i := range boxes {
		out[i] = boxes[i].average()
	}
	return out
}

// colorBox is one axis-aligned box of the median-cut algorithm: a
// contiguous slice of colorCount entries plus their cached RGB bounds and
// total (count-weighted) population.
type colorBox struct {
	colors                             []colorCount
	rMin, rMax, gMin, gMax, bMin, bMax uint8
	total                              int
}

func newColorBox(colors []colorCount) colorBox {
	bx := colorBox{colors: colors}
	if len(colors) == 0 {
		return bx
	}
	bx.rMin, bx.rMax = colors[0].r, colors[0].r
	bx.gMin, bx.gMax = colors[0].g, colors[0].g
	bx.bMin, bx.bMax = colors[0].b, colors[0].b
	for _, c := range colors {
		if c.r < bx.rMin {
			bx.rMin = c.r
		}
		if c.r > bx.rMax {
			bx.rMax = c.r
		}
		if c.g < bx.gMin {
			bx.gMin = c.g
		}
		if c.g > bx.gMax {
			bx.gMax = c.g
		}
		if c.b < bx.bMin {
			bx.bMin = c.b
		}
		if c.b > bx.bMax {
			bx.bMax = c.b
		}
		bx.total += c.count
	}
	return bx
}

// splittable reports whether the box contains more than one distinct color
// (a box with zero range on all three axes cannot be usefully split).
func (bx colorBox) splittable() bool {
	return len(bx.colors) > 1 && (bx.rMax > bx.rMin || bx.gMax > bx.gMin || bx.bMax > bx.bMin)
}

// rgbChannel selects which of a colorCount's channels to compare.
type rgbChannel int

const (
	channelR rgbChannel = iota
	channelG
	channelB
)

// byChannel sorts a []colorCount by a single RGB channel. It implements
// sort.Interface (rather than using sort.Slice) so it compiles under
// TinyGo, which has limited support for the reflection sort.Slice relies
// on internally.
type byChannel struct {
	colors []colorCount
	ch     rgbChannel
}

func (s byChannel) Len() int      { return len(s.colors) }
func (s byChannel) Swap(i, j int) { s.colors[i], s.colors[j] = s.colors[j], s.colors[i] }
func (s byChannel) Less(i, j int) bool {
	switch s.ch {
	case channelR:
		return s.colors[i].r < s.colors[j].r
	case channelG:
		return s.colors[i].g < s.colors[j].g
	default:
		return s.colors[i].b < s.colors[j].b
	}
}

// split partitions bx along its longest RGB axis at the count-weighted
// median, returning two new sub-boxes whose colors together cover bx's
// original colors slice (split in place, then re-sliced).
func (bx colorBox) split() (colorBox, colorBox) {
	rRange := int(bx.rMax) - int(bx.rMin)
	gRange := int(bx.gMax) - int(bx.gMin)
	bRange := int(bx.bMax) - int(bx.bMin)

	switch {
	case rRange >= gRange && rRange >= bRange:
		sort.Sort(byChannel{bx.colors, channelR})
	case gRange >= rRange && gRange >= bRange:
		sort.Sort(byChannel{bx.colors, channelG})
	default:
		sort.Sort(byChannel{bx.colors, channelB})
	}

	half := bx.total / 2
	acc := 0
	mid := 1
	for i, c := range bx.colors {
		acc += c.count
		if acc >= half {
			mid = i + 1
			break
		}
	}
	if mid < 1 {
		mid = 1
	}
	if mid > len(bx.colors)-1 {
		mid = len(bx.colors) - 1
	}

	return newColorBox(bx.colors[:mid]), newColorBox(bx.colors[mid:])
}

// average returns the count-weighted average RGB color of the box, fully
// opaque (transparency is handled separately by the caller via a reserved
// palette slot, not through box averaging).
func (bx colorBox) average() color.NRGBA {
	if bx.total == 0 {
		return color.NRGBA{A: 255}
	}
	var rSum, gSum, bSum uint64
	for _, c := range bx.colors {
		w := uint64(c.count)
		rSum += uint64(c.r) * w
		gSum += uint64(c.g) * w
		bSum += uint64(c.b) * w
	}
	t := uint64(bx.total)
	return color.NRGBA{
		R: uint8(rSum / t),
		G: uint8(gSum / t),
		B: uint8(bSum / t),
		A: 255,
	}
}
