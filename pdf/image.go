package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image/color"
	"image/png"
	"math"
)

const (
	a4Width      = 595.28
	a4Height     = 841.89
	letterWidth  = 612.0
	letterHeight = 792.0
)

// ImagePageOpts configures ImagesToPDF.
type ImagePageOpts struct {
	A4          bool    // legacy: A4 portrait, image scaled to fit and centered
	PageSize    string  // "image", "a4", or "letter"
	Orientation string  // "auto", "portrait", or "landscape"
	Fit         string  // "fit", "fill", "stretch", or "original"
	MarginPt    float64 // margin in PDF points for fixed-size pages
	AutoRotate  bool    // honor JPEG EXIF orientation 3, 6, and 8
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
		xobj, w, h, orientation, err := embedImage(b, data, i)
		if err != nil {
			return nil, err
		}
		if !opts.AutoRotate {
			orientation = 1
		}
		if orientation != 3 && orientation != 6 && orientation != 8 {
			orientation = 1
		}
		placement, err := imagePlacement(float64(w), float64(h), orientation, opts)
		if err != nil {
			return nil, err
		}

		content := imageContent(placement, orientation)
		contentRef := b.alloc()
		b.objs[contentRef.Num-1] = &Stream{Dict: Dict{"Length": len(content)}, Data: []byte(content)}

		pageRef := b.alloc()
		b.objs[pageRef.Num-1] = Dict{
			"Type":      Name("Page"),
			"Parent":    pagesRef,
			"MediaBox":  Array{0, 0, placement.pageW, placement.pageH},
			"Resources": Dict{"XObject": Dict{"Im0": xobj}},
			"Contents":  contentRef,
		}
		kids = append(kids, pageRef)
	}

	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	return b.bytes(catalogRef)
}

type imagePagePlacement struct {
	pageW, pageH       float64
	contentX, contentY float64
	contentW, contentH float64
	drawW, drawH       float64
	x, y               float64
	clip               bool
}

func imagePlacement(rawW, rawH float64, orientation int, opts ImagePageOpts) (imagePagePlacement, error) {
	pw, ph := displayedImageSize(rawW, rawH, orientation)
	cfg := normalizeImagePageOpts(opts)

	if cfg.pageSize == "image" {
		return imagePagePlacement{
			pageW: pw, pageH: ph,
			drawW: pw, drawH: ph,
			x: 0, y: 0,
		}, nil
	}

	pageW, pageH, err := fixedImagePageSize(cfg.pageSize)
	if err != nil {
		return imagePagePlacement{}, err
	}
	if cfg.orientation == "auto" {
		if pw > ph {
			cfg.orientation = "landscape"
		} else {
			cfg.orientation = "portrait"
		}
	}
	switch cfg.orientation {
	case "portrait":
		if pageW > pageH {
			pageW, pageH = pageH, pageW
		}
	case "landscape":
		if pageH > pageW {
			pageW, pageH = pageH, pageW
		}
	default:
		return imagePagePlacement{}, fmt.Errorf("unsupported page orientation %q", cfg.orientation)
	}

	if cfg.marginPt < 0 {
		return imagePagePlacement{}, fmt.Errorf("margin must be non-negative")
	}
	contentW := pageW - cfg.marginPt*2
	contentH := pageH - cfg.marginPt*2
	if contentW <= 0 || contentH <= 0 {
		return imagePagePlacement{}, fmt.Errorf("margin leaves no drawable page area")
	}

	var drawW, drawH float64
	switch cfg.fit {
	case "fit":
		scale := math.Min(contentW/pw, contentH/ph)
		drawW, drawH = pw*scale, ph*scale
	case "fill":
		scale := math.Max(contentW/pw, contentH/ph)
		drawW, drawH = pw*scale, ph*scale
	case "stretch":
		drawW, drawH = contentW, contentH
	case "original":
		drawW, drawH = pw, ph
	default:
		return imagePagePlacement{}, fmt.Errorf("unsupported image fit mode %q", cfg.fit)
	}

	x := cfg.marginPt + (contentW-drawW)/2
	y := cfg.marginPt + (contentH-drawH)/2
	return imagePagePlacement{
		pageW:    pageW,
		pageH:    pageH,
		contentX: cfg.marginPt,
		contentY: cfg.marginPt,
		contentW: contentW,
		contentH: contentH,
		drawW:    drawW,
		drawH:    drawH,
		x:        x,
		y:        y,
		clip:     true,
	}, nil
}

type normalizedImagePageOpts struct {
	pageSize    string
	orientation string
	fit         string
	marginPt    float64
}

func normalizeImagePageOpts(opts ImagePageOpts) normalizedImagePageOpts {
	pageSize := opts.PageSize
	if pageSize == "" {
		if opts.A4 {
			pageSize = "a4"
		} else {
			pageSize = "image"
		}
	}

	orientation := opts.Orientation
	if orientation == "" {
		if opts.A4 && opts.PageSize == "" {
			orientation = "portrait"
		} else {
			orientation = "auto"
		}
	}

	fit := opts.Fit
	if fit == "" {
		fit = "fit"
	}

	return normalizedImagePageOpts{
		pageSize:    pageSize,
		orientation: orientation,
		fit:         fit,
		marginPt:    opts.MarginPt,
	}
}

func fixedImagePageSize(pageSize string) (float64, float64, error) {
	switch pageSize {
	case "a4":
		return a4Width, a4Height, nil
	case "letter":
		return letterWidth, letterHeight, nil
	default:
		return 0, 0, fmt.Errorf("unsupported page size %q", pageSize)
	}
}

func displayedImageSize(rawW, rawH float64, orientation int) (float64, float64) {
	if orientation == 6 || orientation == 8 {
		return rawH, rawW
	}
	return rawW, rawH
}

func imageContent(p imagePagePlacement, orientation int) string {
	var content bytes.Buffer
	content.WriteString("q ")
	if p.clip {
		fmt.Fprintf(&content, "%.2f %.2f %.2f %.2f re W n ", p.contentX, p.contentY, p.contentW, p.contentH)
	}
	content.WriteString(imageMatrix(p, orientation))
	content.WriteString(" cm /Im0 Do Q")
	return content.String()
}

func imageMatrix(p imagePagePlacement, orientation int) string {
	switch orientation {
	case 3:
		return fmt.Sprintf("%.2f %.2f %.2f %.2f %.2f %.2f", -p.drawW, 0.0, 0.0, -p.drawH, p.x+p.drawW, p.y+p.drawH)
	case 6:
		return fmt.Sprintf("%.2f %.2f %.2f %.2f %.2f %.2f", 0.0, -p.drawH, p.drawW, 0.0, p.x, p.y+p.drawH)
	case 8:
		return fmt.Sprintf("%.2f %.2f %.2f %.2f %.2f %.2f", 0.0, p.drawH, -p.drawW, 0.0, p.x+p.drawW, p.y)
	default:
		return fmt.Sprintf("%.2f 0 0 %.2f %.2f %.2f", p.drawW, p.drawH, p.x, p.y)
	}
}

// embedImage sniffs data's format, allocates an Image XObject for it, and
// returns its ref plus pixel dimensions.
func embedImage(b *builder, data []byte, idx int) (ref Ref, w, h, orientation int, err error) {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8:
		return embedJPEG(b, data, idx)
	case len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
		return embedPNG(b, data, idx)
	default:
		return Ref{}, 0, 0, 0, fmt.Errorf("image %d: unsupported format (PNG/JPEG only)", idx)
	}
}

// -------------------------------------------------------------- JPEG ---

// jpegSOF holds the fields decoded from a JPEG SOFn segment.
type jpegSOF struct {
	precision, width, height, ncomp, orientation int
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
	sof := jpegSOF{orientation: 1}
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
		if length < 2 {
			return jpegSOF{}, fmt.Errorf("bad JPEG segment length")
		}
		if pos+length > len(data) {
			return jpegSOF{}, fmt.Errorf("truncated JPEG segment")
		}
		if m == 0xE1 {
			if orientation := exifOrientation(data[pos+2 : pos+length]); orientation != 1 {
				sof.orientation = orientation
			}
		}
		if isSOFMarker(m) {
			if pos+8 > len(data) {
				return jpegSOF{}, fmt.Errorf("truncated SOF segment")
			}
			sof.precision = int(data[pos+2])
			sof.height = int(data[pos+3])<<8 | int(data[pos+4])
			sof.width = int(data[pos+5])<<8 | int(data[pos+6])
			sof.ncomp = int(data[pos+7])
			return sof, nil
		}
		if m == 0xDA { // start of scan: SOF must have appeared earlier
			break
		}
		pos += length
	}
	return jpegSOF{}, fmt.Errorf("no SOF marker found")
}

func exifOrientation(data []byte) int {
	if len(data) < 14 || !bytes.HasPrefix(data, []byte("Exif\x00\x00")) {
		return 1
	}
	tiff := data[6:]
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifdOff := int(order.Uint32(tiff[4:8]))
	if ifdOff < 8 || ifdOff+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifdOff : ifdOff+2]))
	entries := ifdOff + 2
	for i := 0; i < count; i++ {
		entry := entries + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		tag := order.Uint16(tiff[entry : entry+2])
		typ := order.Uint16(tiff[entry+2 : entry+4])
		n := order.Uint32(tiff[entry+4 : entry+8])
		if tag == 0x0112 && typ == 3 && n >= 1 {
			return int(order.Uint16(tiff[entry+8 : entry+10]))
		}
	}
	return 1
}

// embedJPEG embeds the JPEG bytes verbatim (DCTDecode) with no recompression.
func embedJPEG(b *builder, data []byte, idx int) (Ref, int, int, int, error) {
	sof, err := scanJPEGHeader(data)
	if err != nil {
		return Ref{}, 0, 0, 0, fmt.Errorf("image %d: %w", idx, err)
	}
	var cs Name
	switch sof.ncomp {
	case 1:
		cs = "DeviceGray"
	case 3:
		cs = "DeviceRGB"
	case 4:
		return Ref{}, 0, 0, 0, fmt.Errorf("image %d: CMYK JPEG is not supported", idx)
	default:
		return Ref{}, 0, 0, 0, fmt.Errorf("image %d: unsupported JPEG component count %d", idx, sof.ncomp)
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
	return ref, sof.width, sof.height, sof.orientation, nil
}

// --------------------------------------------------------------- PNG ---

// embedPNG decodes the PNG and re-embeds it losslessly as FlateDecode,
// splitting a semi-transparent image into an RGB XObject plus a
// DeviceGray SMask.
//
// ponytail: PNG always embeds as RGB; grayscale pays 3x
func embedPNG(b *builder, data []byte, idx int) (Ref, int, int, int, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Ref{}, 0, 0, 0, fmt.Errorf("image %d: %w", idx, err)
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
	return ref, w, h, 1, nil
}

// zlibDefault zlib-compresses data at compress/zlib's default level.
func zlibDefault(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}
