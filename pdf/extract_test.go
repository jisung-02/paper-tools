package pdf

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/jpeg"
	"io"
	"testing"
)

func TestExtractImages(t *testing.T) {
	// Build a test JPEG image.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 100, A: 255})
		}
	}

	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}

	// Create a PDF with this image using ImagesToPDF.
	pdfBytes, err := ImagesToPDF([][]byte{jpegBuf.Bytes()}, ImagePageOpts{})
	if err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}

	// Extract images from the PDF.
	zipBytes, err := ExtractImages(pdfBytes)
	if err != nil {
		t.Fatalf("ExtractImages: %v", err)
	}

	// Open the ZIP archive.
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	if len(zr.File) != 1 {
		t.Fatalf("expected 1 image in ZIP, got %d", len(zr.File))
	}

	f := zr.File[0]
	if f.Name != "img-001.jpg" {
		t.Errorf("expected filename 'img-001.jpg', got %q", f.Name)
	}

	// Read the entry and verify it's a valid JPEG.
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("open zip entry: %v", err)
	}
	defer rc.Close()

	entryBytes, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read zip entry: %v", err)
	}

	decoded, format, err := image.Decode(bytes.NewReader(entryBytes))
	if err != nil {
		t.Fatalf("decode JPEG from zip entry: %v", err)
	}

	if format != "jpeg" {
		t.Errorf("expected format 'jpeg', got %q", format)
	}

	bounds := decoded.Bounds()
	if bounds.Dx() != 4 || bounds.Dy() != 4 {
		t.Errorf("expected 4x4 image, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}
