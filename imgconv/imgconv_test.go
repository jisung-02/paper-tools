package imgconv

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"
	"testing"
)

type virtualWhiteImage struct{ bounds image.Rectangle }

func (i virtualWhiteImage) ColorModel() color.Model { return color.NRGBAModel }
func (i virtualWhiteImage) Bounds() image.Rectangle { return i.bounds }
func (i virtualWhiteImage) At(int, int) color.Color {
	return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
}

func pngHeader(width, height uint32) []byte {
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8] = 8 // bit depth
	data[9] = 6 // RGBA

	var out bytes.Buffer
	out.Write([]byte{'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n'})
	binary.Write(&out, binary.BigEndian, uint32(len(data)))
	out.WriteString("IHDR")
	out.Write(data)
	var crcInput bytes.Buffer
	crcInput.WriteString("IHDR")
	crcInput.Write(data)
	binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(crcInput.Bytes()))
	return out.Bytes()
}

func TestConvert(t *testing.T) {
	// Build a small test image
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 255})     // Red
	img.SetNRGBA(1, 1, color.NRGBA{R: 0, G: 255, B: 0, A: 255})     // Green
	img.SetNRGBA(2, 2, color.NRGBA{R: 0, G: 0, B: 255, A: 255})     // Blue
	img.SetNRGBA(3, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255}) // White

	// Encode as PNG to get valid image bytes
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("failed to encode test PNG: %v", err)
	}
	pngBytes := pngBuf.Bytes()

	// Test: Convert PNG to JPEG
	t.Run("PNG_to_JPEG", func(t *testing.T) {
		jpegBytes, err := Convert(pngBytes, "jpeg", 85)
		if err != nil {
			t.Fatalf("Convert PNG to JPEG failed: %v", err)
		}
		if jpegBytes == nil || len(jpegBytes) == 0 {
			t.Fatalf("JPEG result is empty")
		}

		// Decode the JPEG result to verify format and dimensions
		decodedImg, format, err := image.Decode(bytes.NewReader(jpegBytes))
		if err != nil {
			t.Fatalf("Failed to decode JPEG result: %v", err)
		}
		if format != "jpeg" {
			t.Fatalf("Expected format 'jpeg', got '%s'", format)
		}
		bounds := decodedImg.Bounds()
		if bounds.Dx() != 4 || bounds.Dy() != 3 {
			t.Fatalf("Expected dimensions 4x3, got %dx%d", bounds.Dx(), bounds.Dy())
		}
	})

	// Test: Convert PNG to GIF
	t.Run("PNG_to_GIF", func(t *testing.T) {
		gifBytes, err := Convert(pngBytes, "gif", 0)
		if err != nil {
			t.Fatalf("Convert PNG to GIF failed: %v", err)
		}
		if gifBytes == nil || len(gifBytes) == 0 {
			t.Fatalf("GIF result is empty")
		}

		// Decode the GIF result to verify format and dimensions
		decodedImg, format, err := image.Decode(bytes.NewReader(gifBytes))
		if err != nil {
			t.Fatalf("Failed to decode GIF result: %v", err)
		}
		if format != "gif" {
			t.Fatalf("Expected format 'gif', got '%s'", format)
		}
		bounds := decodedImg.Bounds()
		if bounds.Dx() != 4 || bounds.Dy() != 3 {
			t.Fatalf("Expected GIF dimensions 4x3, got %dx%d", bounds.Dx(), bounds.Dy())
		}
	})

	// Test: Convert with unsupported format
	t.Run("Unsupported_Format", func(t *testing.T) {
		_, err := Convert(pngBytes, "bogus", 0)
		if err == nil {
			t.Fatalf("Expected error for unsupported format, got nil")
		}
	})

	// Test: Convert PNG to PNG
	t.Run("PNG_to_PNG", func(t *testing.T) {
		pngResult, err := Convert(pngBytes, "png", 0)
		if err != nil {
			t.Fatalf("Convert PNG to PNG failed: %v", err)
		}
		if pngResult == nil || len(pngResult) == 0 {
			t.Fatalf("PNG result is empty")
		}

		// Decode the result
		decodedImg, format, err := image.Decode(bytes.NewReader(pngResult))
		if err != nil {
			t.Fatalf("Failed to decode PNG result: %v", err)
		}
		if format != "png" {
			t.Fatalf("Expected format 'png', got '%s'", format)
		}
		bounds := decodedImg.Bounds()
		if bounds.Dx() != 4 || bounds.Dy() != 3 {
			t.Fatalf("Expected dimensions 4x3, got %dx%d", bounds.Dx(), bounds.Dy())
		}
	})
}

// gradientPNG builds a 4x4 grayscale gradient (value = x*50 + y*10) encoded
// as PNG, and returns its bytes alongside the R value expected in each
// quadrant of a 2x2 box-averaged downscale.
func gradientPNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := uint8(x*50 + y*10)
			img.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode gradient PNG: %v", err)
	}
	return buf.Bytes()
}

func TestResize(t *testing.T) {
	pngBytes := gradientPNG(t)

	t.Run("BoxAverage_Exact", func(t *testing.T) {
		out, format, err := Resize(pngBytes, 2, 2, 0)
		if err != nil {
			t.Fatalf("Resize failed: %v", err)
		}
		if format != "png" {
			t.Fatalf("Expected format 'png', got '%s'", format)
		}

		decoded, _, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("failed to decode resized PNG: %v", err)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() != 2 || bounds.Dy() != 2 {
			t.Fatalf("Expected 2x2, got %dx%d", bounds.Dx(), bounds.Dy())
		}

		// Source values:
		//   0  50 100 150
		//  10  60 110 160
		//  20  70 120 170
		//  30  80 130 180
		// Block (0,0) = {0,50,10,60}/4 = 30, block (1,0) = {100,150,110,160}/4 = 130
		// block (0,1) = {20,70,30,80}/4 = 50, block (1,1) = {120,170,130,180}/4 = 150
		want := [2][2]uint8{{30, 130}, {50, 150}}
		for y := 0; y < 2; y++ {
			for x := 0; x < 2; x++ {
				c := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
				if c.R != want[y][x] {
					t.Fatalf("pixel (%d,%d): expected R=%d, got R=%d", x, y, want[y][x], c.R)
				}
			}
		}
	})

	t.Run("Aspect_Preserved", func(t *testing.T) {
		// 4x4 source constrained to maxW=4, maxH=2 should scale to 2x2
		// (limited by height, aspect ratio 1:1 preserved).
		out, _, err := Resize(pngBytes, 4, 2, 0)
		if err != nil {
			t.Fatalf("Resize failed: %v", err)
		}
		decoded, _, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("failed to decode resized PNG: %v", err)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() != 2 || bounds.Dy() != 2 {
			t.Fatalf("Expected 2x2, got %dx%d", bounds.Dx(), bounds.Dy())
		}
	})

	t.Run("Unconstrained_Axis", func(t *testing.T) {
		// maxH=0 means unconstrained: only maxW=2 applies.
		out, _, err := Resize(pngBytes, 2, 0, 0)
		if err != nil {
			t.Fatalf("Resize failed: %v", err)
		}
		decoded, _, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("failed to decode resized PNG: %v", err)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() != 2 || bounds.Dy() != 2 {
			t.Fatalf("Expected 2x2 (scaled by width only), got %dx%d", bounds.Dx(), bounds.Dy())
		}
	})

	t.Run("No_Upscale", func(t *testing.T) {
		// Source is already smaller than the bounding box: dimensions must
		// stay unchanged, only re-encoded.
		out, _, err := Resize(pngBytes, 100, 100, 0)
		if err != nil {
			t.Fatalf("Resize failed: %v", err)
		}
		decoded, _, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("failed to decode resized PNG: %v", err)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() != 4 || bounds.Dy() != 4 {
			t.Fatalf("Expected unchanged 4x4, got %dx%d", bounds.Dx(), bounds.Dy())
		}
	})

	t.Run("JPEG_RoundTrip", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 30), B: 100, A: 255})
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatalf("failed to encode source JPEG: %v", err)
		}

		out, format, err := Resize(buf.Bytes(), 4, 4, 0)
		if err != nil {
			t.Fatalf("Resize failed: %v", err)
		}
		if format != "jpeg" {
			t.Fatalf("Expected format 'jpeg', got '%s'", format)
		}
		decoded, decFormat, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("failed to decode resized JPEG: %v", err)
		}
		if decFormat != "jpeg" {
			t.Fatalf("Expected decoded format 'jpeg', got '%s'", decFormat)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() != 4 || bounds.Dy() != 4 {
			t.Fatalf("Expected 4x4, got %dx%d", bounds.Dx(), bounds.Dy())
		}
	})

	t.Run("GIF_RoundTrip", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 30), B: 100, A: 255})
			}
		}
		var buf bytes.Buffer
		if err := gif.Encode(&buf, img, nil); err != nil {
			t.Fatalf("failed to encode source GIF: %v", err)
		}

		out, format, err := Resize(buf.Bytes(), 4, 4, 0)
		if err != nil {
			t.Fatalf("Resize failed: %v", err)
		}
		if format != "gif" {
			t.Fatalf("Expected format 'gif', got '%s'", format)
		}
		decoded, decFormat, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("failed to decode resized GIF: %v", err)
		}
		if decFormat != "gif" {
			t.Fatalf("Expected decoded format 'gif', got '%s'", decFormat)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() != 4 || bounds.Dy() != 4 {
			t.Fatalf("Expected 4x4, got %dx%d", bounds.Dx(), bounds.Dy())
		}
	})

	t.Run("Garbage_Input", func(t *testing.T) {
		_, _, err := Resize([]byte("not an image"), 100, 100, 0)
		if err == nil {
			t.Fatalf("Expected error for garbage input, got nil")
		}
	})
}

func TestBoxResizeLargeWhiteImageDoesNotOverflow(t *testing.T) {
	const side = 4105
	white := virtualWhiteImage{bounds: image.Rect(0, 0, side, side)}
	got := boxResize(white, 1, 1)
	c := got.NRGBAAt(0, 0)
	if c != (color.NRGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("boxResize large white image = %#v, want opaque white", c)
	}
}

func TestImagePixelBudgetCheckedBeforeDecode(t *testing.T) {
	data := pngHeader(4105, 4105)
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"Convert", func() error { _, err := Convert(data, "png", 0); return err }},
		{"Resize", func() error { _, _, err := Resize(data, 1, 1, 0); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run()
			if err == nil || !strings.Contains(err.Error(), "pixel budget") {
				t.Fatalf("expected pixel budget error, got %v", err)
			}
		})
	}
}

// photoGradientImage builds a deterministic, photo-like 200x150 test image:
// a radial sine blend on R combined with linear X/Y gradients on G/B. It has
// far more distinct colors than a flat-color test image, which is what
// makes it a meaningful stand-in for a photograph when comparing palette
// quantizers.
func photoGradientImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	cx, cy := float64(w)/2, float64(h)/2
	maxDist := math.Hypot(cx, cy)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			dist := math.Hypot(dx, dy) / maxDist

			r := 0.5 + 0.5*math.Sin(dist*2*math.Pi)
			if r < 0 {
				r = 0
			} else if r > 1 {
				r = 1
			}

			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r * 255),
				G: uint8(float64(x) / float64(w) * 255),
				B: uint8(float64(y) / float64(h) * 255),
				A: 255,
			})
		}
	}
	return img
}

// rmsAgainst computes the per-channel (R,G,B) RMS error of dec against src
// over src's bounds.
func rmsAgainst(t *testing.T, src, dec image.Image) float64 {
	t.Helper()
	b := src.Bounds()
	var sum float64
	var n int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c1 := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			c2 := color.NRGBAModel.Convert(dec.At(x, y)).(color.NRGBA)
			dr := float64(c1.R) - float64(c2.R)
			dg := float64(c1.G) - float64(c2.G)
			db := float64(c1.B) - float64(c2.B)
			sum += dr*dr + dg*dg + db*db
			n += 3
		}
	}
	return math.Sqrt(sum / float64(n))
}

// TestGIFQuantizerBeatsPlan9Baseline verifies the median-cut quantizer used
// by Convert/Resize produces a meaningfully closer palette than the
// stdlib's default fixed Plan9 palette (gif.Encode with nil Options) for
// photo-like content. Measured on this test image: baseline RMS ~= 21.6,
// median-cut RMS ~= 10.1 (roughly half), so a new < old*0.8 margin is
// comfortably below what's actually observed.
func TestGIFQuantizerBeatsPlan9Baseline(t *testing.T) {
	img := photoGradientImage(200, 150)

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("failed to encode source PNG: %v", err)
	}

	// Baseline: stdlib default (Plan9 fixed palette, no custom quantizer).
	var baseBuf bytes.Buffer
	if err := gif.Encode(&baseBuf, img, nil); err != nil {
		t.Fatalf("baseline gif.Encode failed: %v", err)
	}
	baseDec, err := gif.Decode(bytes.NewReader(baseBuf.Bytes()))
	if err != nil {
		t.Fatalf("failed to decode baseline GIF: %v", err)
	}
	oldRMS := rmsAgainst(t, img, baseDec)

	// New: Convert's median-cut adaptive quantizer.
	newBytes, err := Convert(pngBuf.Bytes(), "gif", 0)
	if err != nil {
		t.Fatalf("Convert to GIF failed: %v", err)
	}
	newDec, err := gif.Decode(bytes.NewReader(newBytes))
	if err != nil {
		t.Fatalf("failed to decode Convert's GIF: %v", err)
	}
	newRMS := rmsAgainst(t, img, newDec)

	t.Logf("baseline (Plan9) RMS = %f, median-cut RMS = %f", oldRMS, newRMS)

	if newRMS >= oldRMS*0.8 {
		t.Fatalf("expected median-cut RMS (%f) to be well below baseline*0.8 (%f, baseline=%f)", newRMS, oldRMS*0.8, oldRMS)
	}
}

// TestGIFTransparencyPreserved verifies that an image with a transparent
// region still decodes as transparent after a round trip through Convert's
// GIF encoding path (GIF only supports 1-bit transparency, so this checks
// that the quantizer reserves a transparent palette slot rather than
// dissolving alpha into an opaque nearby color).
func TestGIFTransparencyPreserved(t *testing.T) {
	const w, h = 40, 40
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				// Opaque half: a color gradient.
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 6), G: uint8(y * 6), B: 128, A: 255})
			} else {
				// Transparent half.
				img.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 0})
			}
		}
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("failed to encode source PNG: %v", err)
	}

	gifBytes, err := Convert(pngBuf.Bytes(), "gif", 0)
	if err != nil {
		t.Fatalf("Convert to GIF failed: %v", err)
	}

	dec, err := gif.Decode(bytes.NewReader(gifBytes))
	if err != nil {
		t.Fatalf("failed to decode GIF: %v", err)
	}

	for y := 0; y < h; y++ {
		for x := w / 2; x < w; x++ {
			c := color.NRGBAModel.Convert(dec.At(x, y)).(color.NRGBA)
			if c.A != 0 {
				t.Fatalf("pixel (%d,%d): expected fully transparent (A=0), got A=%d", x, y, c.A)
			}
		}
	}

	// Sanity check the opaque half actually stayed opaque.
	for y := 0; y < h; y++ {
		for x := 0; x < w/2; x++ {
			c := color.NRGBAModel.Convert(dec.At(x, y)).(color.NRGBA)
			if c.A != 255 {
				t.Fatalf("pixel (%d,%d): expected fully opaque (A=255), got A=%d", x, y, c.A)
			}
		}
	}
}
