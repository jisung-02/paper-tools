package pdf

import "testing"

func TestVisualGeometryQuarterTurnsAndOrigins(t *testing.T) {
	for _, tc := range []struct {
		r          int
		u, v, x, y float64
	}{{0, 10, 20, 110, 220}, {90, 10, 20, 280, 210}, {180, 10, 20, 290, 380}, {270, 10, 20, 120, 390}} {
		g := pageVisualGeometry{x0: 100, y0: 200, x1: 300, y1: 400, width: 200, height: 200, rotate: tc.r}
		if tc.r == 90 || tc.r == 270 {
			g.width, g.height = 200, 200
		}
		x, y := g.visualPoint(tc.u, tc.v)
		if x != tc.x || y != tc.y {
			t.Errorf("rotate %d: got %.0f,%.0f want %.0f,%.0f", tc.r, x, y, tc.x, tc.y)
		}
	}
}

func TestVisualPointRotate90UsesUnrotatedWidth(t *testing.T) {
	g := pageVisualGeometry{x0: 100, y0: 200, x1: 300, y1: 500, width: 300, height: 200, rotate: 90}
	x, y := g.visualPoint(10, 20)
	if x != 280 || y != 210 {
		t.Fatalf("got %.0f,%.0f want 280,210", x, y)
	}
}
