package imgconv

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

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
