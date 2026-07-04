package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image/color"
	"image/png"
	"math"
)

const (
	a4Width  = 595.28
	a4Height = 841.89
)

// ImagePageOpts configures ImagesToPDF.
type ImagePageOpts struct {
	A4 bool // true: every page is A4 portrait, image scaled to fit and centered; false: page size = image pixel size in points (72 dpi)
}

// ImagesToPDF builds a PDF with one image per page. PNG and JPEG only.
func ImagesToPDF(images [][]byte, opts ImagePageOpts) ([]byte, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("at least 1 image required")
	}

	b := &builder{}
	catalogRef := b.alloc()
	pagesRef := b.alloc()

	var kids Array
	for i, data := range images {
		xobj, w, h, err := embedImage(b, data, i)
		if err != nil {
			return nil, err
		}
		pw, ph := float64(w), float64(h)

		var pageW, pageH, drawW, drawH, x, y float64
		if opts.A4 {
			pageW, pageH = a4Width, a4Height
			scale := math.Min(pageW/pw, pageH/ph)
			drawW, drawH = pw*scale, ph*scale
			x, y = (pageW-drawW)/2, (pageH-drawH)/2
		} else {
			pageW, pageH = pw, ph
			drawW, drawH = pw, ph
		}

		content := fmt.Sprintf("q %.2f 0 0 %.2f %.2f %.2f cm /Im0 Do Q", drawW, drawH, x, y)
		contentRef := b.alloc()
		b.objs[contentRef.Num-1] = &Stream{Dict: Dict{"Length": len(content)}, Data: []byte(content)}

		pageRef := b.alloc()
		b.objs[pageRef.Num-1] = Dict{
			"Type":      Name("Page"),
			"Parent":    pagesRef,
			"MediaBox":  Array{0, 0, pageW, pageH},
			"Resources": Dict{"XObject": Dict{"Im0": xobj}},
			"Contents":  contentRef,
		}
		kids = append(kids, pageRef)
	}

	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	return b.bytes(catalogRef), nil
}

// embedImage sniffs data's format, allocates an Image XObject for it, and
// returns its ref plus pixel dimensions.
func embedImage(b *builder, data []byte, idx int) (ref Ref, w, h int, err error) {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8:
		return embedJPEG(b, data, idx)
	case len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
		return embedPNG(b, data, idx)
	default:
		return Ref{}, 0, 0, fmt.Errorf("image %d: unsupported format (PNG/JPEG only)", idx)
	}
}

// -------------------------------------------------------------- JPEG ---

// jpegSOF holds the fields decoded from a JPEG SOFn segment.
type jpegSOF struct {
	precision, width, height, ncomp int
}

// isSOFMarker reports whether m is one of the frame-header markers
// (C0-CF except C4/DHT, C8/JPG, CC/DAC).
func isSOFMarker(m byte) bool {
	return m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC
}

// scanJPEGHeader walks JPEG segments to find the SOF marker without
// decoding pixel data, so the original bytes can be embedded as-is.
func scanJPEGHeader(data []byte) (jpegSOF, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return jpegSOF{}, fmt.Errorf("not a JPEG")
	}
	pos := 2
	for pos < len(data) {
		if data[pos] != 0xFF {
			return jpegSOF{}, fmt.Errorf("malformed JPEG marker at offset %d", pos)
		}
		pos++
		for pos < len(data) && data[pos] == 0xFF { // fill bytes
			pos++
		}
		if pos >= len(data) {
			break
		}
		m := data[pos]
		pos++
		if m == 0xD8 || m == 0xD9 || m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			continue // no length field
		}
		if pos+2 > len(data) {
			break
		}
		length := int(data[pos])<<8 | int(data[pos+1])
		if isSOFMarker(m) {
			if pos+7 > len(data) {
				return jpegSOF{}, fmt.Errorf("truncated SOF segment")
			}
			return jpegSOF{
				precision: int(data[pos+2]),
				height:    int(data[pos+3])<<8 | int(data[pos+4]),
				width:     int(data[pos+5])<<8 | int(data[pos+6]),
				ncomp:     int(data[pos+7]),
			}, nil
		}
		if m == 0xDA { // start of scan: SOF must have appeared earlier
			break
		}
		pos += length
	}
	return jpegSOF{}, fmt.Errorf("no SOF marker found")
}

// embedJPEG embeds the JPEG bytes verbatim (DCTDecode) with no recompression.
func embedJPEG(b *builder, data []byte, idx int) (Ref, int, int, error) {
	sof, err := scanJPEGHeader(data)
	if err != nil {
		return Ref{}, 0, 0, fmt.Errorf("image %d: %w", idx, err)
	}
	var cs Name
	switch sof.ncomp {
	case 1:
		cs = "DeviceGray"
	case 3:
		cs = "DeviceRGB"
	case 4:
		return Ref{}, 0, 0, fmt.Errorf("image %d: CMYK JPEG is not supported", idx)
	default:
		return Ref{}, 0, 0, fmt.Errorf("image %d: unsupported JPEG component count %d", idx, sof.ncomp)
	}

	ref := b.alloc()
	b.objs[ref.Num-1] = &Stream{
		Dict: Dict{
			"Type":             Name("XObject"),
			"Subtype":          Name("Image"),
			"Width":            sof.width,
			"Height":           sof.height,
			"BitsPerComponent": sof.precision,
			"ColorSpace":       cs,
			"Filter":           Name("DCTDecode"),
			"Length":           len(data),
		},
		Data: data,
	}
	return ref, sof.width, sof.height, nil
}

// --------------------------------------------------------------- PNG ---

// embedPNG decodes the PNG and re-embeds it losslessly as FlateDecode,
// splitting a semi-transparent image into an RGB XObject plus a
// DeviceGray SMask.
//
// ponytail: PNG always embeds as RGB; grayscale pays 3x
func embedPNG(b *builder, data []byte, idx int) (Ref, int, int, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Ref{}, 0, 0, fmt.Errorf("image %d: %w", idx, err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	rgb := make([]byte, w*h*3)
	alpha := make([]byte, w*h)
	hasAlpha := false
	i := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			rgb[i*3], rgb[i*3+1], rgb[i*3+2] = c.R, c.G, c.B
			alpha[i] = c.A
			if c.A != 255 {
				hasAlpha = true
			}
			i++
		}
	}

	rgbComp := zlibDefault(rgb)
	dict := Dict{
		"Type":             Name("XObject"),
		"Subtype":          Name("Image"),
		"Width":            w,
		"Height":           h,
		"BitsPerComponent": 8,
		"ColorSpace":       Name("DeviceRGB"),
		"Filter":           Name("FlateDecode"),
		"Length":           len(rgbComp),
	}
	if hasAlpha {
		alphaComp := zlibDefault(alpha)
		smaskRef := b.alloc()
		b.objs[smaskRef.Num-1] = &Stream{
			Dict: Dict{
				"Type":             Name("XObject"),
				"Subtype":          Name("Image"),
				"Width":            w,
				"Height":           h,
				"BitsPerComponent": 8,
				"ColorSpace":       Name("DeviceGray"),
				"Filter":           Name("FlateDecode"),
				"Length":           len(alphaComp),
			},
			Data: alphaComp,
		}
		dict["SMask"] = smaskRef
	}

	ref := b.alloc()
	b.objs[ref.Num-1] = &Stream{Dict: dict, Data: rgbComp}
	return ref, w, h, nil
}

// zlibDefault zlib-compresses data at compress/zlib's default level.
func zlibDefault(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}
