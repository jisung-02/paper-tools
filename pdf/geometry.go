package pdf

// pageVisualGeometry describes the unrotated PDF box and the visual size a
// viewer presents after applying /Rotate.
type pageVisualGeometry struct {
	x0, y0, x1, y1, width, height float64
	rotate                        int
}

func visualGeometry(rect func(any) (float64, float64, float64, float64, bool), pd Dict) pageVisualGeometry {
	x0, y0, x1, y1, ok := rect(pd["CropBox"])
	if !ok {
		x0, y0, x1, y1, ok = rect(pd["MediaBox"])
	}
	if !ok {
		x0, y0, x1, y1 = 0, 0, 612, 792
	}
	r, _ := rnum(pd["Rotate"])
	rot := ((int(r) % 360) + 360) % 360
	g := pageVisualGeometry{x0: x0, y0: y0, x1: x1, y1: y1, width: x1 - x0, height: y1 - y0, rotate: rot}
	if rot == 90 || rot == 270 {
		g.width, g.height = g.height, g.width
	}
	return g
}

// visualPoint maps coordinates in the viewer's top-level visual box back to
// PDF user space, preserving non-zero box origins for all quarter turns.
func (g pageVisualGeometry) visualPoint(u, v float64) (float64, float64) {
	switch g.rotate {
	case 90:
		return g.x1 - v, g.y0 + u
	case 180:
		return g.x1 - u, g.y1 - v
	case 270:
		return g.x0 + v, g.y1 - u
	default:
		return g.x0 + u, g.y0 + v
	}
}
