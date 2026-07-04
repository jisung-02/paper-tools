package imgconv

import (
	"bytes"
	"image"
	"image/color"
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
