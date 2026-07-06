package pdf

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"image/jpeg"
)

// CompressOpts configures Compress.
type CompressOpts struct {
	JPEGQuality int // 1-100; 0 -> 70
	MaxWidth    int // downsample images wider than this many pixels; 0 -> 1600
	Grayscale   bool
}

// Compress rewrites file, recompressing its images and streams to shrink
// file size. It drops per-page Metadata/PieceInfo/Thumb entries; unreferenced
// objects (e.g. an unused catalog-level Metadata) are dropped for free by the
// page-rooted reachability GC in buildDoc.
func Compress(file []byte, opts CompressOpts) ([]byte, error) {
	if opts.JPEGQuality <= 0 {
		opts.JPEGQuality = 70
	}
	if opts.MaxWidth <= 0 {
		opts.MaxWidth = 1600
	}
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}

	mut := func(b *builder, pageIndex int, pd Dict, m map[int]Ref) error {
		delete(pd, "Metadata")
		delete(pd, "PieceInfo")
		delete(pd, "Thumb")
		return nil
	}
	b, root, err := buildDoc([]*Doc{d}, [][]Page{pages}, mut)
	if err != nil {
		return nil, err
	}

	for i, v := range b.objs {
		st, ok := v.(*Stream)
		if !ok {
			continue
		}
		b.objs[i] = recodeStream(b, st, opts)
	}

	return b.bytes(root), nil
}

// filterName returns the single filter name applied to st: absent -> ("",
// true); a lone Name or single-element Array -> (name, true); anything else
// (multiple filters, etc.) -> ("", false).
func filterName(b *builder, st *Stream) (Name, bool) {
	switch f := b.rv(st.Dict["Filter"]).(type) {
	case nil:
		return "", true
	case Name:
		return f, true
	case Array:
		if len(f) == 1 {
			if nm, ok := b.rv(f[0]).(Name); ok {
				return nm, true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// recodeStream returns a possibly-smaller replacement for st, or st itself
// unchanged if no rule applies or nothing adopted was actually smaller.
func recodeStream(b *builder, st *Stream, opts CompressOpts) *Stream {
	fname, simple := filterName(b, st)
	if !simple {
		return st
	}
	if im, _ := b.rv(st.Dict["ImageMask"]).(bool); im {
		return st
	}
	if t, _ := b.rv(st.Dict["Type"]).(Name); t == "XRef" || t == "ObjStm" {
		return st
	}
	isImage := false
	if sub, _ := b.rv(st.Dict["Subtype"]).(Name); sub == "Image" {
		isImage = true
	}

	switch {
	case isImage && fname == "DCTDecode":
		if r := recodeJPEGImage(b, st, opts); r != nil {
			return r
		}
		return st
	case fname == "FlateDecode":
		if isImage {
			if r := tryFlateImageToJPEG(b, st, opts); r != nil {
				return r
			}
		}
		if r := recodeFlate(b, st); r != nil {
			return r
		}
		return st
	case fname == "":
		if r := recodeNoFilter(st); r != nil {
			return r
		}
		return st
	default:
		return st
	}
}

// recodeJPEGImage re-encodes an existing DCTDecode image at opts.JPEGQuality,
// downsampling first if it's wider than opts.MaxWidth. Returns nil (keep
// original) on decode failure, a /Decode array, or if the result isn't
// smaller.
func recodeJPEGImage(b *builder, st *Stream, opts CompressOpts) *Stream {
	if _, ok := st.Dict["Decode"]; ok {
		return nil
	}
	if _, ok := st.Dict["SMask"]; ok {
		return nil
	}
	if _, ok := st.Dict["Mask"]; ok {
		return nil
	}
	img, err := jpeg.Decode(bytes.NewReader(st.Data))
	if err != nil {
		return nil
	}
	return reencodeAsJPEG(st, img, opts)
}

// tryFlateImageToJPEG converts an uncompressed-pixel FlateDecode image into
// a lossy JPEG when that's expected to pay off: plain 8-bit RGB/Gray data,
// no soft mask, no stencil mask, no Decode inversion, and big enough that
// JPEG's overhead is worth it.
func tryFlateImageToJPEG(b *builder, st *Stream, opts CompressOpts) *Stream {
	if _, ok := st.Dict["Decode"]; ok {
		return nil
	}
	if _, ok := st.Dict["SMask"]; ok {
		return nil
	}
	if _, ok := st.Dict["Mask"]; ok {
		return nil
	}
	if bpc, ok := b.rv(st.Dict["BitsPerComponent"]).(int); !ok || bpc != 8 {
		return nil
	}
	w, wok := b.rv(st.Dict["Width"]).(int)
	h, hok := b.rv(st.Dict["Height"]).(int)
	if !wok || !hok || w*h < 65536 {
		return nil
	}
	comps, gray, ok := csComponents(b, st.Dict["ColorSpace"])
	if !ok {
		return nil
	}

	decoded, err := decodeStreamWith(b.rv, st)
	if err != nil || len(decoded) != w*h*comps {
		return nil
	}

	var img image.Image
	if gray {
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
	return reencodeAsJPEG(st, img, opts)
}

// csComponents reports the component count and grayness of a color space
// that is DeviceRGB, DeviceGray, or ICCBased with N == 3 or N == 1.
func csComponents(b *builder, csv any) (comps int, gray bool, ok bool) {
	switch t := b.rv(csv).(type) {
	case Name:
		switch t {
		case "DeviceRGB":
			return 3, false, true
		case "DeviceGray":
			return 1, true, true
		}
	case Array:
		if len(t) != 2 {
			return 0, false, false
		}
		if nm, ok := b.rv(t[0]).(Name); !ok || nm != "ICCBased" {
			return 0, false, false
		}
		ref, ok := t[1].(Ref)
		if !ok {
			return 0, false, false
		}
		st, ok := b.rv(ref).(*Stream)
		if !ok {
			return 0, false, false
		}
		switch n, _ := b.rv(st.Dict["N"]).(int); n {
		case 3:
			return 3, false, true
		case 1:
			return 1, true, true
		}
	}
	return 0, false, false
}

// reencodeAsJPEG downsamples img (if wider than opts.MaxWidth) and
// JPEG-encodes it, returning a replacement stream only if strictly smaller
// than st.Data.
func reencodeAsJPEG(st *Stream, img image.Image, opts CompressOpts) *Stream {
	if opts.Grayscale {
		img = grayscaleImage(img)
	}
	bounds := img.Bounds()
	if bounds.Dx() > opts.MaxWidth {
		img = downscale(img, opts.MaxWidth)
		bounds = img.Bounds()
	}
	w, h := bounds.Dx(), bounds.Dy()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: opts.JPEGQuality}); err != nil {
		return nil
	}
	if buf.Len() >= len(st.Data) {
		return nil
	}

	_, isGray := img.(*image.Gray)
	cs := Name("DeviceRGB")
	if isGray {
		cs = "DeviceGray"
	}

	nd := make(Dict, len(st.Dict))
	for k, v := range st.Dict {
		nd[k] = v
	}
	delete(nd, "DecodeParms")
	delete(nd, "DP")
	nd["Width"] = w
	nd["Height"] = h
	nd["BitsPerComponent"] = 8
	nd["ColorSpace"] = cs
	nd["Filter"] = Name("DCTDecode")
	nd["Length"] = buf.Len()
	return &Stream{Dict: nd, Data: buf.Bytes()}
}

func grayscaleImage(img image.Image) image.Image {
	b := img.Bounds()
	if g, ok := img.(*image.Gray); ok && b.Min.X == 0 && b.Min.Y == 0 {
		return g
	}
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.SetGray(x-b.Min.X, y-b.Min.Y, color.GrayModel.Convert(img.At(x, y)).(color.Gray))
		}
	}
	return out
}

// downscale bilinearly resamples img down to width maxW, preserving aspect
// ratio. img wider than maxW is a precondition; callers check that.
func downscale(img image.Image, maxW int) image.Image {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	nw := maxW
	nh := max(1, int(float64(sh)*float64(nw)/float64(sw)))

	if g, ok := img.(*image.Gray); ok {
		return resampleGray(g, nw, nh)
	}
	return resampleNRGBA(toNRGBA(img), nw, nh)
}

func toNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.SetNRGBA(x-b.Min.X, y-b.Min.Y, color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA))
		}
	}
	return out
}

func resampleNRGBA(src *image.NRGBA, nw, nh int) *image.NRGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := float64(y) * float64(sh-1) / float64(max(nh-1, 1))
		y0 := int(sy)
		y1 := min(y0+1, sh-1)
		fy := sy - float64(y0)
		for x := 0; x < nw; x++ {
			sx := float64(x) * float64(sw-1) / float64(max(nw-1, 1))
			x0 := int(sx)
			x1 := min(x0+1, sw-1)
			fx := sx - float64(x0)
			c00, c10 := src.NRGBAAt(x0, y0), src.NRGBAAt(x1, y0)
			c01, c11 := src.NRGBAAt(x0, y1), src.NRGBAAt(x1, y1)
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(bilerp(float64(c00.R), float64(c10.R), float64(c01.R), float64(c11.R), fx, fy)),
				G: uint8(bilerp(float64(c00.G), float64(c10.G), float64(c01.G), float64(c11.G), fx, fy)),
				B: uint8(bilerp(float64(c00.B), float64(c10.B), float64(c01.B), float64(c11.B), fx, fy)),
				A: uint8(bilerp(float64(c00.A), float64(c10.A), float64(c01.A), float64(c11.A), fx, fy)),
			})
		}
	}
	return dst
}

func resampleGray(src *image.Gray, nw, nh int) *image.Gray {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewGray(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := float64(y) * float64(sh-1) / float64(max(nh-1, 1))
		y0 := int(sy)
		y1 := min(y0+1, sh-1)
		fy := sy - float64(y0)
		for x := 0; x < nw; x++ {
			sx := float64(x) * float64(sw-1) / float64(max(nw-1, 1))
			x0 := int(sx)
			x1 := min(x0+1, sw-1)
			fx := sx - float64(x0)
			v00, v10 := float64(src.GrayAt(x0, y0).Y), float64(src.GrayAt(x1, y0).Y)
			v01, v11 := float64(src.GrayAt(x0, y1).Y), float64(src.GrayAt(x1, y1).Y)
			dst.SetGray(x, y, color.Gray{Y: uint8(bilerp(v00, v10, v01, v11, fx, fy))})
		}
	}
	return dst
}

func bilerp(v00, v10, v01, v11, fx, fy float64) float64 {
	top := v00 + (v10-v00)*fx
	bot := v01 + (v11-v01)*fx
	return top + (bot-top)*fy
}

// recodeFlate decodes an arbitrary FlateDecode stream (dropping any
// predictor) and re-compresses at the best ratio, adopting only if smaller.
func recodeFlate(b *builder, st *Stream) *Stream {
	const maxDecoded = 64 << 20
	decoded, err := decodeStreamWith(b.rv, st)
	if err != nil || len(decoded) > maxDecoded {
		return nil
	}
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	zw.Write(decoded)
	zw.Close()
	if buf.Len() >= len(st.Data) {
		return nil
	}
	nd := make(Dict, len(st.Dict))
	for k, v := range st.Dict {
		nd[k] = v
	}
	delete(nd, "DecodeParms")
	delete(nd, "DP")
	nd["Length"] = buf.Len()
	return &Stream{Dict: nd, Data: buf.Bytes()}
}

// recodeNoFilter deflates a raw (unfiltered) stream, adopting only if smaller.
func recodeNoFilter(st *Stream) *Stream {
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	zw.Write(st.Data)
	zw.Close()
	if buf.Len() >= len(st.Data) {
		return nil
	}
	nd := make(Dict, len(st.Dict)+1)
	for k, v := range st.Dict {
		nd[k] = v
	}
	nd["Filter"] = Name("FlateDecode")
	nd["Length"] = buf.Len()
	return &Stream{Dict: nd, Data: buf.Bytes()}
}
