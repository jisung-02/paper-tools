package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrInvalidRasterPage reports malformed PNG data, geometry, or budget configuration.
	ErrInvalidRasterPage = errors.New("invalid raster PDF page")
	// ErrRasterPDFBudget reports valid pages exceeding a configured resource budget.
	ErrRasterPDFBudget = errors.New("raster PDF exceeds budget")
	// ErrRasterPDFInvariant reports an internal whitelist verification failure.
	ErrRasterPDFInvariant = errors.New("raster-only PDF invariant failed")
	// ErrRasterPDFLifecycle reports use of a closed, poisoned, or incomplete encoder.
	ErrRasterPDFLifecycle = errors.New("invalid raster PDF encoder lifecycle")
)

const (
	defaultRasterMaxPages        = 500
	hardRasterMaxPages           = 2000
	defaultRasterMaxPagePixels   = 16 * 1024 * 1024
	hardRasterMaxPagePixels      = 32 * 1024 * 1024
	defaultRasterMaxPixels       = 64 * 1024 * 1024
	hardRasterMaxPixels          = 128 * 1024 * 1024
	defaultRasterMaxPagePNGBytes = 64 * 1024 * 1024
	hardRasterMaxPagePNGBytes    = 128 * 1024 * 1024
	defaultRasterMaxPNGBytes     = 256 * 1024 * 1024
	hardRasterMaxPNGBytes        = 512 * 1024 * 1024
	defaultRasterMaxOutputBytes  = 256 * 1024 * 1024
	hardRasterMaxOutputBytes     = 512 * 1024 * 1024
	maxRasterPagePoints          = 14400
)

// RasterPage is one already-redacted lossless PNG and its explicit PDF display size.
type RasterPage struct {
	PNGData  []byte  `json:"pngData"`
	WidthPt  float64 `json:"widthPt"`
	HeightPt float64 `json:"heightPt"`
}

// RasterPDFOpts bounds work and output; zero fields select conservative defaults.
type RasterPDFOpts struct {
	MaxPages        int    `json:"maxPages"`
	MaxPagePixels   uint64 `json:"maxPagePixels"`
	MaxPixels       uint64 `json:"maxPixels"`
	MaxPagePNGBytes uint64 `json:"maxPagePNGBytes"`
	MaxPNGBytes     uint64 `json:"maxPNGBytes"`
	MaxOutputBytes  uint64 `json:"maxOutputBytes"`
}

// RasterPDFLimits is the validated, fully resolved form of RasterPDFOpts.
// Bridge sessions use it to reject declared page and output sizes before
// allocating their staging buffers.
type RasterPDFLimits struct {
	MaxPages        int
	MaxPagePixels   uint64
	MaxPixels       uint64
	MaxPagePNGBytes uint64
	MaxPNGBytes     uint64
	MaxOutputBytes  uint64
}

type resolvedRasterLimits struct {
	pages        int
	pagePixels   uint64
	pixels       uint64
	pagePNGBytes uint64
	pngBytes     uint64
	outBytes     uint64
}

type rasterLimitBuffer struct {
	bytes.Buffer
	limit uint64
}

func (b *rasterLimitBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - uint64(b.Len())
	if uint64(len(p)) > remaining {
		if remaining > 0 {
			_, _ = b.Buffer.Write(p[:int(remaining)])
		}
		return int(remaining), ErrRasterPDFBudget
	}
	return b.Buffer.Write(p)
}

func rasterIntLimit(value, fallback, hard int, name string) (int, error) {
	if value < 0 || value > hard {
		return 0, fmt.Errorf("%w: %s must be between 0 and %d", ErrInvalidRasterPage, name, hard)
	}
	if value == 0 {
		return fallback, nil
	}
	return value, nil
}

func rasterUintLimit(value, fallback, hard uint64, name string) (uint64, error) {
	if value > hard {
		return 0, fmt.Errorf("%w: %s must not exceed %d", ErrInvalidRasterPage, name, hard)
	}
	if value == 0 {
		return fallback, nil
	}
	return value, nil
}

func resolveRasterLimits(opts RasterPDFOpts) (resolvedRasterLimits, error) {
	pages, err := rasterIntLimit(opts.MaxPages, defaultRasterMaxPages, hardRasterMaxPages, "MaxPages")
	if err != nil {
		return resolvedRasterLimits{}, err
	}
	pagePixels, err := rasterUintLimit(opts.MaxPagePixels, defaultRasterMaxPagePixels, hardRasterMaxPagePixels, "MaxPagePixels")
	if err != nil {
		return resolvedRasterLimits{}, err
	}
	pixels, err := rasterUintLimit(opts.MaxPixels, defaultRasterMaxPixels, hardRasterMaxPixels, "MaxPixels")
	if err != nil {
		return resolvedRasterLimits{}, err
	}
	pagePNGBytes, err := rasterUintLimit(opts.MaxPagePNGBytes, defaultRasterMaxPagePNGBytes, hardRasterMaxPagePNGBytes, "MaxPagePNGBytes")
	if err != nil {
		return resolvedRasterLimits{}, err
	}
	pngBytes, err := rasterUintLimit(opts.MaxPNGBytes, defaultRasterMaxPNGBytes, hardRasterMaxPNGBytes, "MaxPNGBytes")
	if err != nil {
		return resolvedRasterLimits{}, err
	}
	outBytes, err := rasterUintLimit(opts.MaxOutputBytes, defaultRasterMaxOutputBytes, hardRasterMaxOutputBytes, "MaxOutputBytes")
	if err != nil {
		return resolvedRasterLimits{}, err
	}
	return resolvedRasterLimits{
		pages: pages, pagePixels: pagePixels, pixels: pixels,
		pagePNGBytes: pagePNGBytes, pngBytes: pngBytes, outBytes: outBytes,
	}, nil
}

// ResolveRasterPDFLimits validates opts and returns all defaulted limits.
func ResolveRasterPDFLimits(opts RasterPDFOpts) (RasterPDFLimits, error) {
	limits, err := resolveRasterLimits(opts)
	if err != nil {
		return RasterPDFLimits{}, err
	}
	return RasterPDFLimits{
		MaxPages:        limits.pages,
		MaxPagePixels:   limits.pagePixels,
		MaxPixels:       limits.pixels,
		MaxPagePNGBytes: limits.pagePNGBytes,
		MaxPNGBytes:     limits.pngBytes,
		MaxOutputBytes:  limits.outBytes,
	}, nil
}

func checkedRasterProduct(a, b uint64) (uint64, bool) {
	if a != 0 && b > math.MaxUint64/a {
		return 0, false
	}
	return a * b, true
}

func addWithinRasterBudget(total, value, limit uint64) (uint64, bool) {
	if value > limit || total > limit-value {
		return 0, false
	}
	return total + value, true
}

func formatPDFNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func compositeRasterChannel(channel, alpha uint8) byte {
	return byte((uint32(channel)*uint32(alpha) + 255*uint32(255-alpha) + 127) / 255)
}

func rasterCompressedUpperBound(rgbBytes uint64) (uint64, bool) {
	overhead := rgbBytes/1000 + 64*1024
	if rgbBytes > math.MaxUint64-overhead {
		return 0, false
	}
	return rgbBytes + overhead, true
}

func rasterPDFGraphOverhead(pageCount int) (uint64, bool) {
	pages := uint64(pageCount)
	if pages > (math.MaxUint64-4096)/1024 {
		return 0, false
	}
	return 4096 + pages*1024, true
}

func pngBitDepth(data []byte) (byte, bool) {
	if len(data) < 25 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) ||
		!bytes.Equal(data[12:16], []byte("IHDR")) {
		return 0, false
	}
	return data[24], true
}

func encodeOpaqueRasterPNG(data []byte, index int, maxCompressedBytes uint64) ([]byte, int, int, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("image %d: %w", index, err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	compressed := &rasterLimitBuffer{limit: maxCompressedBytes}
	zw := zlib.NewWriter(compressed)
	row := make([]byte, width*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		offset := 0
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			row[offset] = compositeRasterChannel(pixel.R, pixel.A)
			row[offset+1] = compositeRasterChannel(pixel.G, pixel.A)
			row[offset+2] = compositeRasterChannel(pixel.B, pixel.A)
			offset += 3
		}
		if _, err := zw.Write(row); err != nil {
			_ = zw.Close()
			return nil, 0, 0, fmt.Errorf("%w: image %d compressed data", ErrRasterPDFBudget, index)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, 0, 0, fmt.Errorf("%w: image %d compressed data", ErrRasterPDFBudget, index)
	}
	return compressed.Bytes(), width, height, nil
}

type rasterPDFState uint8

const (
	rasterPDFOpen rasterPDFState = iota
	rasterPDFPoisoned
	rasterPDFFinished
	rasterPDFAborted
)

const rasterPDFHeader = "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n"

// RasterPDFEncoder writes a raster-only PDF page-by-page while retaining only
// the object offsets and page references needed to finish the page tree.
type RasterPDFEncoder struct {
	w             io.Writer
	limits        resolvedRasterLimits
	expectedPages int
	pageCount     int
	offsets       []uint64
	kids          []Ref
	written       uint64
	totalPixels   uint64
	totalPNGBytes uint64
	estimatedOut  uint64
	state         rasterPDFState
}

// NewRasterPDFEncoder starts a raster-only PDF that must receive exactly
// expectedPages calls to AddPage before Finish.
func NewRasterPDFEncoder(w io.Writer, expectedPages int, opts RasterPDFOpts) (*RasterPDFEncoder, error) {
	if w == nil {
		return nil, fmt.Errorf("%w: output writer is required", ErrInvalidRasterPage)
	}
	limits, err := resolveRasterLimits(opts)
	if err != nil {
		return nil, err
	}
	if expectedPages <= 0 {
		return nil, fmt.Errorf("%w: at least one page is required", ErrInvalidRasterPage)
	}
	if expectedPages > limits.pages {
		return nil, fmt.Errorf("%w: pages %d exceed %d", ErrRasterPDFBudget, expectedPages, limits.pages)
	}
	graphOverhead, ok := rasterPDFGraphOverhead(expectedPages)
	if !ok || graphOverhead > limits.outBytes {
		return nil, fmt.Errorf("%w: output size overflow", ErrRasterPDFBudget)
	}
	e := &RasterPDFEncoder{
		w:             w,
		limits:        limits,
		expectedPages: expectedPages,
		offsets:       make([]uint64, expectedPages*3+3),
		kids:          make([]Ref, 0, expectedPages),
		estimatedOut:  graphOverhead,
		state:         rasterPDFOpen,
	}
	if err := e.writeRaw([]byte(rasterPDFHeader)); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *RasterPDFEncoder) lifecycleError(operation string) error {
	return fmt.Errorf("%w: cannot %s in state %d", ErrRasterPDFLifecycle, operation, e.state)
}

func (e *RasterPDFEncoder) writeRaw(data []byte) error {
	if e.written > e.limits.outBytes || uint64(len(data)) > e.limits.outBytes-e.written {
		e.state = rasterPDFPoisoned
		return fmt.Errorf("%w: output bytes exceed %d", ErrRasterPDFBudget, e.limits.outBytes)
	}
	n, err := e.w.Write(data)
	e.written += uint64(n)
	if err != nil || n != len(data) {
		e.state = rasterPDFPoisoned
		if err == nil {
			err = io.ErrShortWrite
		}
		return err
	}
	return nil
}

type rasterEncoderWriter struct{ encoder *RasterPDFEncoder }

func (w rasterEncoderWriter) Write(p []byte) (int, error) {
	if err := w.encoder.writeRaw(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func writeRasterName(w io.Writer, name Name) error {
	var out strings.Builder
	out.Grow(len(name) + 1)
	out.WriteByte('/')
	for index := 0; index < len(name); index++ {
		char := name[index]
		if char <= 0x20 || char >= 0x7f || isDelim(char) || char == '#' {
			fmt.Fprintf(&out, "#%02X", char)
		} else {
			out.WriteByte(char)
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func writeRasterValue(w io.Writer, value any) error {
	switch value := value.(type) {
	case nil:
		_, err := io.WriteString(w, "null")
		return err
	case bool:
		if value {
			_, err := io.WriteString(w, "true")
			return err
		}
		_, err := io.WriteString(w, "false")
		return err
	case int:
		_, err := io.WriteString(w, strconv.Itoa(value))
		return err
	case float64:
		_, err := io.WriteString(w, strconv.FormatFloat(value, 'f', -1, 64))
		return err
	case Ref:
		_, err := fmt.Fprintf(w, "%d %d R", value.Num, value.Gen)
		return err
	case Name:
		return writeRasterName(w, value)
	case String:
		if _, err := io.WriteString(w, "<"); err != nil {
			return err
		}
		for _, char := range value {
			if _, err := fmt.Fprintf(w, "%02X", char); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, ">")
		return err
	case Array:
		if _, err := io.WriteString(w, "["); err != nil {
			return err
		}
		for index, item := range value {
			if index > 0 {
				if _, err := io.WriteString(w, " "); err != nil {
					return err
				}
			}
			if err := writeRasterValue(w, item); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "]")
		return err
	case Dict:
		if _, err := io.WriteString(w, "<<"); err != nil {
			return err
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, string(key))
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err := io.WriteString(w, " "); err != nil {
				return err
			}
			if err := writeRasterName(w, Name(key)); err != nil {
				return err
			}
			if _, err := io.WriteString(w, " "); err != nil {
				return err
			}
			if err := writeRasterValue(w, value[Name(key)]); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, " >>")
		return err
	case *Stream:
		if err := writeRasterValue(w, value.Dict); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\nstream\n"); err != nil {
			return err
		}
		if _, err := w.Write(value.Data); err != nil {
			return err
		}
		_, err := io.WriteString(w, "\nendstream")
		return err
	default:
		_, err := io.WriteString(w, "null")
		return err
	}
}

func (e *RasterPDFEncoder) writeObject(number int, value any) error {
	if number <= 0 || number >= len(e.offsets) {
		e.state = rasterPDFPoisoned
		return fmt.Errorf("%w: invalid object number %d", ErrRasterPDFInvariant, number)
	}
	e.offsets[number] = e.written
	w := rasterEncoderWriter{encoder: e}
	if _, err := fmt.Fprintf(w, "%d 0 obj\n", number); err != nil {
		return err
	}
	if err := writeRasterValue(w, value); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\nendobj\n")
	return err
}

// AddPage validates and immediately writes one image, content stream, and page object.
func (e *RasterPDFEncoder) AddPage(page RasterPage) error {
	if e == nil || e.state != rasterPDFOpen {
		if e == nil {
			return fmt.Errorf("%w: nil encoder", ErrRasterPDFLifecycle)
		}
		return e.lifecycleError("add a page")
	}
	if e.pageCount >= e.expectedPages {
		e.state = rasterPDFPoisoned
		return fmt.Errorf("%w: received more than %d pages", ErrRasterPDFLifecycle, e.expectedPages)
	}
	pageNumber := e.pageCount + 1
	if math.IsNaN(page.WidthPt) || math.IsInf(page.WidthPt, 0) || page.WidthPt <= 0 || page.WidthPt > maxRasterPagePoints ||
		math.IsNaN(page.HeightPt) || math.IsInf(page.HeightPt, 0) || page.HeightPt <= 0 || page.HeightPt > maxRasterPagePoints {
		return fmt.Errorf("%w: page %d dimensions must be finite, positive, and at most %dpt", ErrInvalidRasterPage, pageNumber, maxRasterPagePoints)
	}
	if len(page.PNGData) == 0 {
		return fmt.Errorf("%w: page %d has no PNG data", ErrInvalidRasterPage, pageNumber)
	}
	if uint64(len(page.PNGData)) > e.limits.pagePNGBytes {
		return fmt.Errorf("%w: page %d PNG bytes exceed %d", ErrRasterPDFBudget, pageNumber, e.limits.pagePNGBytes)
	}
	nextPNGBytes, ok := addWithinRasterBudget(e.totalPNGBytes, uint64(len(page.PNGData)), e.limits.pngBytes)
	if !ok {
		return fmt.Errorf("%w: PNG bytes exceed %d", ErrRasterPDFBudget, e.limits.pngBytes)
	}
	config, err := png.DecodeConfig(bytes.NewReader(page.PNGData))
	if err != nil {
		return fmt.Errorf("%w: page %d PNG: %v", ErrInvalidRasterPage, pageNumber, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("%w: page %d PNG has invalid dimensions", ErrInvalidRasterPage, pageNumber)
	}
	if depth, ok := pngBitDepth(page.PNGData); !ok || depth == 16 {
		return fmt.Errorf("%w: page %d PNG must use at most 8 bits per channel", ErrInvalidRasterPage, pageNumber)
	}
	pixels, ok := checkedRasterProduct(uint64(config.Width), uint64(config.Height))
	if !ok || pixels > e.limits.pagePixels {
		return fmt.Errorf("%w: page %d pixels exceed %d", ErrRasterPDFBudget, pageNumber, e.limits.pagePixels)
	}
	nextPixels, ok := addWithinRasterBudget(e.totalPixels, pixels, e.limits.pixels)
	if !ok {
		return fmt.Errorf("%w: total pixels exceed %d", ErrRasterPDFBudget, e.limits.pixels)
	}
	rgbBytes, ok := checkedRasterProduct(pixels, 3)
	if !ok {
		return fmt.Errorf("%w: page %d RGB size overflow", ErrRasterPDFBudget, pageNumber)
	}
	maxCompressed, ok := rasterCompressedUpperBound(rgbBytes)
	if !ok {
		return fmt.Errorf("%w: page %d compressed size overflow", ErrRasterPDFBudget, pageNumber)
	}
	nextEstimated, ok := addWithinRasterBudget(e.estimatedOut, maxCompressed, e.limits.outBytes)
	if !ok {
		return fmt.Errorf("%w: estimated output bytes exceed %d", ErrRasterPDFBudget, e.limits.outBytes)
	}
	compressed, width, height, err := encodeOpaqueRasterPNG(page.PNGData, pageNumber, maxCompressed)
	if err != nil {
		if errors.Is(err, ErrRasterPDFBudget) {
			return err
		}
		return fmt.Errorf("%w: page %d PNG decode: %v", ErrInvalidRasterPage, pageNumber, err)
	}
	if width != config.Width || height != config.Height {
		return fmt.Errorf("%w: page %d PNG dimensions changed during decode", ErrInvalidRasterPage, pageNumber)
	}

	imageRef := Ref{Num: 3 + e.pageCount*3}
	contentRef := Ref{Num: imageRef.Num + 1}
	pageRef := Ref{Num: imageRef.Num + 2}
	image := &Stream{Dict: Dict{
		"Type": Name("XObject"), "Subtype": Name("Image"),
		"Width": width, "Height": height, "BitsPerComponent": 8,
		"ColorSpace": Name("DeviceRGB"), "Filter": Name("FlateDecode"), "Length": len(compressed),
	}, Data: compressed}
	contentData := []byte("q " + formatPDFNumber(page.WidthPt) + " 0 0 " + formatPDFNumber(page.HeightPt) + " 0 0 cm /Im0 Do Q")
	content := &Stream{Dict: Dict{"Length": len(contentData)}, Data: contentData}
	pageDict := Dict{
		"Type":      Name("Page"),
		"Parent":    Ref{Num: 2},
		"MediaBox":  Array{0, 0, page.WidthPt, page.HeightPt},
		"Resources": Dict{"XObject": Dict{"Im0": imageRef}},
		"Contents":  contentRef,
	}
	if err := e.writeObject(imageRef.Num, image); err != nil {
		return err
	}
	if err := e.writeObject(contentRef.Num, content); err != nil {
		return err
	}
	if err := e.writeObject(pageRef.Num, pageDict); err != nil {
		return err
	}
	e.kids = append(e.kids, pageRef)
	e.pageCount = pageNumber
	e.totalPixels = nextPixels
	e.totalPNGBytes = nextPNGBytes
	e.estimatedOut = nextEstimated
	return nil
}

// Finish writes the page tree, catalog, cross-reference table, and trailer.
func (e *RasterPDFEncoder) Finish() error {
	if e == nil || e.state != rasterPDFOpen {
		if e == nil {
			return fmt.Errorf("%w: nil encoder", ErrRasterPDFLifecycle)
		}
		return e.lifecycleError("finish")
	}
	if e.pageCount != e.expectedPages {
		e.state = rasterPDFPoisoned
		return fmt.Errorf("%w: got %d pages, expected %d", ErrRasterPDFLifecycle, e.pageCount, e.expectedPages)
	}
	kids := make(Array, len(e.kids))
	for i, kid := range e.kids {
		kids[i] = kid
	}
	if err := e.writeObject(2, Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}); err != nil {
		return err
	}
	if err := e.writeObject(1, Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}}); err != nil {
		return err
	}
	xrefOffset := e.written
	var tail bytes.Buffer
	fmt.Fprintf(&tail, "xref\n0 %d\n", len(e.offsets))
	tail.WriteString("0000000000 65535 f \n")
	for number := 1; number < len(e.offsets); number++ {
		if e.offsets[number] == 0 {
			e.state = rasterPDFPoisoned
			return fmt.Errorf("%w: object %d was not written", ErrRasterPDFInvariant, number)
		}
		fmt.Fprintf(&tail, "%010d 00000 n \n", e.offsets[number])
	}
	fmt.Fprintf(&tail, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", len(e.offsets), xrefOffset)
	if err := e.writeRaw(tail.Bytes()); err != nil {
		return err
	}
	e.state = rasterPDFFinished
	return nil
}

// Abort discards encoder bookkeeping. It is safe to call repeatedly in every state.
func (e *RasterPDFEncoder) Abort() {
	if e == nil {
		return
	}
	e.state = rasterPDFAborted
	e.offsets = nil
	e.kids = nil
}

// BuildRasterOnlyPDF creates and verifies a whitelisted PDF without accepting source PDF objects.
func BuildRasterOnlyPDF(pages []RasterPage, opts RasterPDFOpts) ([]byte, error) {
	var out bytes.Buffer
	encoder, err := NewRasterPDFEncoder(&out, len(pages), opts)
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		if err := encoder.AddPage(page); err != nil {
			encoder.Abort()
			return nil, err
		}
	}
	if err := encoder.Finish(); err != nil {
		encoder.Abort()
		return nil, err
	}
	if err := ValidateRasterOnlyPDF(out.Bytes(), len(pages)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func rasterExactKeys(dict Dict, required ...Name) bool {
	if len(dict) != len(required) {
		return false
	}
	for _, key := range required {
		if _, ok := dict[key]; !ok {
			return false
		}
	}
	return true
}

func validateRasterImage(d *Doc, ref Ref, allowed map[int]bool) error {
	if allowed[ref.Num] {
		return fmt.Errorf("image object %d is reused unexpectedly", ref.Num)
	}
	allowed[ref.Num] = true
	stream, ok := d.Get(ref.Num).(*Stream)
	if !ok {
		return fmt.Errorf("image object %d is not a stream", ref.Num)
	}
	required := []Name{"Type", "Subtype", "Width", "Height", "BitsPerComponent", "ColorSpace", "Filter", "Length"}
	if !rasterExactKeys(stream.Dict, required...) {
		return fmt.Errorf("image object %d has non-whitelisted keys", ref.Num)
	}
	if typ, _ := d.R(stream.Dict["Type"]).(Name); typ != "XObject" {
		return fmt.Errorf("image object %d has invalid Type", ref.Num)
	}
	if subtype, _ := d.R(stream.Dict["Subtype"]).(Name); subtype != "Image" {
		return fmt.Errorf("image object %d has invalid Subtype", ref.Num)
	}
	if filter, _ := d.R(stream.Dict["Filter"]).(Name); filter != "FlateDecode" {
		return fmt.Errorf("image object %d has invalid Filter", ref.Num)
	}
	if bits, ok := rnum(d.R(stream.Dict["BitsPerComponent"])); !ok || bits != 8 {
		return fmt.Errorf("image object %d has invalid BitsPerComponent", ref.Num)
	}
	w, wok := rnum(d.R(stream.Dict["Width"]))
	h, hok := rnum(d.R(stream.Dict["Height"]))
	if !wok || !hok || w <= 0 || h <= 0 || w != math.Trunc(w) || h != math.Trunc(h) {
		return fmt.Errorf("image object %d has invalid dimensions", ref.Num)
	}
	if colorSpace, _ := d.R(stream.Dict["ColorSpace"]).(Name); colorSpace != "DeviceRGB" {
		return fmt.Errorf("image object %d has invalid ColorSpace", ref.Num)
	}
	length, lengthOK := rnum(d.R(stream.Dict["Length"]))
	if !lengthOK || length != math.Trunc(length) || length != float64(len(stream.Data)) {
		return fmt.Errorf("image object %d has invalid Length", ref.Num)
	}
	pixels, ok := checkedRasterProduct(uint64(w), uint64(h))
	if !ok || pixels > hardRasterMaxPagePixels {
		return fmt.Errorf("image object %d has invalid pixel count", ref.Num)
	}
	expected, ok := checkedRasterProduct(pixels, 3)
	if !ok || expected > uint64(math.MaxInt) {
		return fmt.Errorf("image object %d decoded size overflows", ref.Num)
	}
	decoded, err := decodeStreamWithLimit(d.R, stream, int(expected))
	if err != nil || uint64(len(decoded)) != expected {
		return fmt.Errorf("image object %d has invalid decoded RGB size", ref.Num)
	}
	return nil
}

// ValidateRasterOnlyPDF proves serialization introduced no object outside the raster-only graph.
func ValidateRasterOnlyPDF(data []byte, wantPages int) error {
	return ValidateRasterOnlyPDFStream(bytes.NewReader(data), wantPages, RasterPDFValidationLimits{
		MaxInputBytes: uint64(len(data)),
	})
}

// validateRasterOnlyPDF is kept for package-local callers and compatibility with existing tests.
func validateRasterOnlyPDF(data []byte, wantPages int) error {
	return ValidateRasterOnlyPDF(data, wantPages)
}
