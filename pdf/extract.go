package pdf

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/png"
)

// ExtractImages walks the PDF's pages and extracts all image XObjects,
// returning them in a ZIP archive. Returns an error if no images found.
func ExtractImages(file []byte) ([]byte, error) {
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}

	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}

	// Deduplicate images by Ref (Num).
	seen := make(map[int]bool)
	var images []*extractedImage // preserve order

	// Walk every page's Resources -> XObject dict.
	for _, page := range pages {
		res, ok := d.R(page.Attrs["Resources"]).(Dict)
		if !ok {
			continue
		}

		xobjs, ok := d.R(res["XObject"]).(Dict)
		if !ok {
			continue
		}

		// Iterate through XObject entries.
		for _, xval := range xobjs {
			ref, ok := xval.(Ref)
			if !ok {
				continue
			}

			// Skip if we've already seen this Ref.
			if seen[ref.Num] {
				continue
			}
			seen[ref.Num] = true

			st, ok := d.Get(ref.Num).(*Stream)
			if !ok {
				continue
			}

			// Check if it's an image.
			if d.R(st.Dict["Subtype"]) != Name("Image") {
				continue
			}

			// Extract the image based on its filter.
			img, err := extractImage(d, st)
			if err != nil {
				// Skip on error (unsupported format).
				continue
			}
			if img != nil {
				images = append(images, img)
			}
		}
	}

	if len(images) == 0 {
		return nil, fmt.Errorf("no extractable images found")
	}

	// Build ZIP archive.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for i, img := range images {
		name := fmt.Sprintf("img-%03d.%s", i+1, img.ext)
		w, err := zw.Create(name)
		if err != nil {
			zw.Close()
			return nil, err
		}
		if _, err := w.Write(img.data); err != nil {
			zw.Close()
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

type extractedImage struct {
	ext  string // "jpg" or "png"
	data []byte
}

// extractImage extracts the image stream based on its Filter, returning nil
// for unsupported formats (which are silently skipped).
func extractImage(d *Doc, st *Stream) (*extractedImage, error) {
	filter := d.R(st.Dict["Filter"])
	filterName, simple := getFilterName(d, filter)

	if !simple {
		// ponytail: multiple filters, complex filter chains unsupported; skip
		return nil, nil
	}

	switch filterName {
	case "DCTDecode":
		// Raw JPEG bytes.
		return &extractedImage{ext: "jpg", data: st.Data}, nil

	case "FlateDecode":
		// Decode flate data and check if it's 8-bit RGB/Gray.
		return extractFlateImage(d, st)

	default:
		// ponytail: CCITTFaxDecode, JBIG2Decode, JPXDecode, indexed/Separation color spaces, CMYK, etc. unsupported; skip
		return nil, nil
	}
}

// getFilterName extracts the filter name from st.Dict["Filter"].
// Returns ("", true) if no filter, (name, true) if a single filter,
// ("", false) if multiple filters or unsupported structure.
func getFilterName(d *Doc, filter any) (Name, bool) {
	filter = d.R(filter)

	switch f := filter.(type) {
	case nil:
		return "", true
	case Name:
		return f, true
	case Array:
		if len(f) == 1 {
			if nm, ok := d.R(f[0]).(Name); ok {
				return nm, true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// extractFlateImage decodes a FlateDecode stream and converts it to PNG if possible.
func extractFlateImage(d *Doc, st *Stream) (*extractedImage, error) {
	bpc, ok := d.R(st.Dict["BitsPerComponent"]).(int)
	if !ok || bpc != 8 {
		// ponytail: non-8-bit or missing BitsPerComponent unsupported; skip
		return nil, nil
	}

	w, wok := d.R(st.Dict["Width"]).(int)
	h, hok := d.R(st.Dict["Height"]).(int)
	if !wok || !hok || w <= 0 || h <= 0 {
		return nil, nil
	}

	// Determine color space.
	cs := d.R(st.Dict["ColorSpace"])
	csName, ok := cs.(Name)
	if !ok {
		// ponytail: complex color spaces (ICCBased, indexed, separation, CMYK, etc.) unsupported; skip
		return nil, nil
	}

	var isGray bool
	switch csName {
	case "DeviceRGB":
		isGray = false
	case "DeviceGray":
		isGray = true
	default:
		// ponytail: other color spaces unsupported; skip
		return nil, nil
	}

	// Decode the stream.
	decoded, err := d.decodeStream(st)
	if err != nil {
		return nil, nil // Skip on decode error.
	}

	expectedLen := w * h
	if !isGray {
		expectedLen *= 3
	}

	if len(decoded) != expectedLen {
		return nil, nil
	}

	// Build the image.
	var img image.Image
	if isGray {
		g := image.NewGray(image.Rect(0, 0, w, h))
		copy(g.Pix, decoded)
		img = g
	} else {
		nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < w*h; i++ {
			nrgba.Pix[i*4] = decoded[i*3]
			nrgba.Pix[i*4+1] = decoded[i*3+1]
			nrgba.Pix[i*4+2] = decoded[i*3+2]
			nrgba.Pix[i*4+3] = 255
		}
		img = nrgba
	}

	// Encode to PNG.
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, nil
	}

	return &extractedImage{ext: "png", data: buf.Bytes()}, nil
}
