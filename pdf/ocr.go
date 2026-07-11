package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrInvalidOCRInput reports malformed OCR data or unsafe configuration.
	ErrInvalidOCRInput = errors.New("invalid OCR input")
	// ErrOCRBudget reports valid OCR input that exceeds a configured budget.
	ErrOCRBudget = errors.New("OCR input exceeds budget")
)

const (
	defaultOCRMaxPages       = 1000
	defaultOCRMaxWords       = 250000
	defaultOCRMaxCharacters  = 4_000_000
	defaultOCRMaxInputBytes  = 128 * 1024 * 1024
	defaultOCRMaxFontBytes   = 16 * 1024 * 1024
	defaultOCRMaxOutputBytes = 192 * 1024 * 1024
	hardOCRMaxPages          = 2000
	hardOCRMaxWords          = 500000
	hardOCRMaxCharacters     = 8_000_000
	hardOCRMaxInputBytes     = 512 * 1024 * 1024
	hardOCRMaxFontBytes      = 64 * 1024 * 1024
	hardOCRMaxOutputBytes    = 512 * 1024 * 1024
)

// OCRWord uses rendered-page coordinates and confidence normalized to 0..1.
type OCRWord struct {
	Text       string  `json:"text"`
	Left       float64 `json:"left"`
	Top        float64 `json:"top"`
	Right      float64 `json:"right"`
	Bottom     float64 `json:"bottom"`
	Confidence float64 `json:"confidence"`
}

// OCRPage contains words for the source PDF page at the same slice index.
type OCRPage struct {
	Words []OCRWord `json:"words"`
}

// OCRLayerOpts controls confidence filtering and bounded work; zero budgets select safe defaults.
type OCRLayerOpts struct {
	MinConfidence  float64 `json:"minConfidence"`
	MaxPages       int     `json:"maxPages"`
	MaxWords       int     `json:"maxWords"`
	MaxCharacters  int     `json:"maxCharacters"`
	MaxInputBytes  uint64  `json:"maxInputBytes"`
	MaxFontBytes   uint64  `json:"maxFontBytes"`
	MaxOutputBytes uint64  `json:"maxOutputBytes"`
}

type resolvedOCRLimits struct {
	pages, words, characters           int
	inputBytes, fontBytes, outputBytes uint64
}

func ocrByteLimit(value, fallback, hard uint64, name string) (uint64, error) {
	if value > hard {
		return 0, fmt.Errorf("%w: %s must not exceed %d", ErrInvalidOCRInput, name, hard)
	}
	if value == 0 {
		return fallback, nil
	}
	return value, nil
}

type ocrWordLayout struct {
	word             OCRWord
	fontSize         float64
	a, b, c, d, e, f float64
}

func ocrLimit(value, fallback, hard int, name string) (int, error) {
	if value < 0 || value > hard {
		return 0, fmt.Errorf("%w: %s must be between 0 and %d", ErrInvalidOCRInput, name, hard)
	}
	if value == 0 {
		return fallback, nil
	}
	return value, nil
}

func resolveOCRLimits(opts OCRLayerOpts) (resolvedOCRLimits, error) {
	pages, err := ocrLimit(opts.MaxPages, defaultOCRMaxPages, hardOCRMaxPages, "MaxPages")
	if err != nil {
		return resolvedOCRLimits{}, err
	}
	words, err := ocrLimit(opts.MaxWords, defaultOCRMaxWords, hardOCRMaxWords, "MaxWords")
	if err != nil {
		return resolvedOCRLimits{}, err
	}
	characters, err := ocrLimit(opts.MaxCharacters, defaultOCRMaxCharacters, hardOCRMaxCharacters, "MaxCharacters")
	if err != nil {
		return resolvedOCRLimits{}, err
	}
	inputBytes, err := ocrByteLimit(opts.MaxInputBytes, defaultOCRMaxInputBytes, hardOCRMaxInputBytes, "MaxInputBytes")
	if err != nil {
		return resolvedOCRLimits{}, err
	}
	fontBytes, err := ocrByteLimit(opts.MaxFontBytes, defaultOCRMaxFontBytes, hardOCRMaxFontBytes, "MaxFontBytes")
	if err != nil {
		return resolvedOCRLimits{}, err
	}
	outputBytes, err := ocrByteLimit(opts.MaxOutputBytes, defaultOCRMaxOutputBytes, hardOCRMaxOutputBytes, "MaxOutputBytes")
	if err != nil {
		return resolvedOCRLimits{}, err
	}
	return resolvedOCRLimits{pages: pages, words: words, characters: characters, inputBytes: inputBytes, fontBytes: fontBytes, outputBytes: outputBytes}, nil
}

func validOCRText(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func finiteInUnit(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func sourcePageGeometry(d *Doc, page Page) (pageVisualGeometry, error) {
	x0, y0, x1, y1, ok := docRect(d, page.Attrs["CropBox"])
	if !ok {
		x0, y0, x1, y1, ok = docRect(d, page.Attrs["MediaBox"])
	}
	if !ok {
		x0, y0, x1, y1 = 0, 0, 612, 792
	}
	for _, value := range []float64{x0, y0, x1, y1} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return pageVisualGeometry{}, fmt.Errorf("%w: page box contains a non-finite coordinate", ErrInvalidOCRInput)
		}
	}
	if x1 <= x0 || y1 <= y0 {
		return pageVisualGeometry{}, fmt.Errorf("%w: page box has no area", ErrInvalidOCRInput)
	}
	rotation := 0
	if raw, exists := page.Attrs["Rotate"]; exists {
		value, ok := rnum(d.R(raw))
		if !ok {
			return pageVisualGeometry{}, fmt.Errorf("%w: page rotation is not numeric", ErrInvalidOCRInput)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
			return pageVisualGeometry{}, fmt.Errorf("%w: page rotation must be an integer quarter turn", ErrInvalidOCRInput)
		}
		rotation = int(math.Mod(value, 360))
		if rotation < 0 {
			rotation += 360
		}
	}
	if rotation != 0 && rotation != 90 && rotation != 180 && rotation != 270 {
		return pageVisualGeometry{}, fmt.Errorf("%w: unsupported page rotation %d", ErrInvalidOCRInput, rotation)
	}
	g := pageVisualGeometry{
		x0: x0, y0: y0, x1: x1, y1: y1,
		width: x1 - x0, height: y1 - y0, rotate: rotation,
	}
	if rotation == 90 || rotation == 270 {
		g.width, g.height = g.height, g.width
	}
	if math.IsNaN(g.width) || math.IsInf(g.width, 0) || math.IsNaN(g.height) || math.IsInf(g.height, 0) || g.width <= 0 || g.height <= 0 {
		return pageVisualGeometry{}, fmt.Errorf("%w: page box dimensions are invalid", ErrInvalidOCRInput)
	}
	return g, nil
}

func ocrTextMatrix(g pageVisualGeometry, word OCRWord, font *ttfFont) (ocrWordLayout, error) {
	boxW := (word.Right - word.Left) * g.width
	boxH := (word.Bottom - word.Top) * g.height
	if boxW <= 0 || boxH <= 0 || math.IsInf(boxW, 0) || math.IsInf(boxH, 0) {
		return ocrWordLayout{}, fmt.Errorf("%w: OCR word box has invalid page-space dimensions", ErrInvalidOCRInput)
	}
	metricHeight := font.ascender - font.descender
	if font.unitsPerEm <= 0 || metricHeight <= 0 {
		return ocrWordLayout{}, fmt.Errorf("%w: font has invalid vertical metrics", ErrInvalidOCRInput)
	}
	fontSize := boxH * float64(font.unitsPerEm) / float64(metricHeight)
	naturalW := lineWidth(font, []rune(word.Text), fontSize)
	if naturalW <= 0 || math.IsNaN(naturalW) || math.IsInf(naturalW, 0) {
		return ocrWordLayout{}, fmt.Errorf("%w: OCR word has no usable font width", ErrInvalidOCRInput)
	}
	sx := boxW / naturalW
	if sx <= 0 || math.IsNaN(sx) || math.IsInf(sx, 0) {
		return ocrWordLayout{}, fmt.Errorf("%w: OCR word horizontal scale is invalid", ErrInvalidOCRInput)
	}

	// Map the top-left OCR box through inverse page rotation and fit the font advance to its width.
	u := word.Left * g.width
	v := (1-word.Bottom)*g.height - float64(font.descender)*fontSize/float64(font.unitsPerEm)
	var e, f float64
	switch g.rotate {
	case 90:
		e, f = g.x1-v, g.y0+u
	case 180:
		e, f = g.x1-u, g.y1-v
	case 270:
		e, f = g.x0+v, g.y1-u
	default:
		e, f = g.x0+u, g.y0+v
	}
	layout := ocrWordLayout{word: word, fontSize: fontSize, e: e, f: f}
	switch g.rotate {
	case 90:
		layout.a, layout.b, layout.c, layout.d = 0, sx, -1, 0
	case 180:
		layout.a, layout.b, layout.c, layout.d = -sx, 0, 0, -1
	case 270:
		layout.a, layout.b, layout.c, layout.d = 0, -sx, 1, 0
	default:
		layout.a, layout.b, layout.c, layout.d = sx, 0, 0, 1
	}
	return layout, nil
}

// Clone nested fonts because an indirect Resources object's inline Font dictionary can remain shared.
func ocrOwnedFonts(b *builder, resources Dict) Dict {
	fonts := Dict{}
	if existing, ok := b.rv(resources["Font"]).(Dict); ok {
		fonts = make(Dict, len(existing)+1)
		for name, value := range existing {
			fonts[name] = value
		}
	}
	resources["Font"] = fonts
	return fonts
}

// AddOCRTextLayer adds invisible searchable Type0 text while preserving existing page streams.
func AddOCRTextLayer(file, fontTTF []byte, pages []OCRPage, opts OCRLayerOpts) ([]byte, error) {
	if math.IsNaN(opts.MinConfidence) || math.IsInf(opts.MinConfidence, 0) || opts.MinConfidence < 0 || opts.MinConfidence > 1 {
		return nil, fmt.Errorf("%w: MinConfidence must be between 0 and 1", ErrInvalidOCRInput)
	}
	limits, err := resolveOCRLimits(opts)
	if err != nil {
		return nil, err
	}
	if uint64(len(file)) > limits.inputBytes {
		return nil, fmt.Errorf("%w: input bytes exceed %d", ErrOCRBudget, limits.inputBytes)
	}
	if uint64(len(fontTTF)) > limits.fontBytes {
		return nil, fmt.Errorf("%w: font bytes exceed %d", ErrOCRBudget, limits.fontBytes)
	}
	doc, err := Parse(file)
	if err != nil {
		return nil, fmt.Errorf("%w: PDF: %v", ErrInvalidOCRInput, err)
	}
	sourcePages, err := doc.Pages()
	if err != nil {
		return nil, fmt.Errorf("%w: pages: %v", ErrInvalidOCRInput, err)
	}
	if len(pages) != len(sourcePages) {
		return nil, fmt.Errorf("%w: got OCR for %d pages, PDF has %d", ErrInvalidOCRInput, len(pages), len(sourcePages))
	}
	if len(pages) > limits.pages {
		return nil, fmt.Errorf("%w: pages %d exceed %d", ErrOCRBudget, len(pages), limits.pages)
	}
	for pageIndex, page := range sourcePages {
		if err := materializeInheritedPageAttrs(doc, page); err != nil {
			return nil, fmt.Errorf("%w: page %d: %v", ErrInvalidOCRInput, pageIndex+1, err)
		}
	}

	geometries := make([]pageVisualGeometry, len(sourcePages))
	for i, page := range sourcePages {
		geometry, err := sourcePageGeometry(doc, page)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", i+1, err)
		}
		geometries[i] = geometry
	}

	wordCount, characterCount := 0, 0
	for pageIndex, page := range pages {
		for wordIndex, word := range page.Words {
			if !validOCRText(word.Text) {
				return nil, fmt.Errorf("%w: page %d word %d has invalid text", ErrInvalidOCRInput, pageIndex+1, wordIndex+1)
			}
			if !finiteInUnit(word.Left) || !finiteInUnit(word.Top) || !finiteInUnit(word.Right) || !finiteInUnit(word.Bottom) || word.Left >= word.Right || word.Top >= word.Bottom {
				return nil, fmt.Errorf("%w: page %d word %d has invalid normalized bounds", ErrInvalidOCRInput, pageIndex+1, wordIndex+1)
			}
			if math.IsNaN(word.Confidence) || math.IsInf(word.Confidence, 0) || word.Confidence < 0 || word.Confidence > 1 {
				return nil, fmt.Errorf("%w: page %d word %d confidence must be between 0 and 1", ErrInvalidOCRInput, pageIndex+1, wordIndex+1)
			}
			wordCount++
			if wordCount > limits.words {
				return nil, fmt.Errorf("%w: words exceed %d", ErrOCRBudget, limits.words)
			}
			characterCount += utf8.RuneCountInString(word.Text)
			if characterCount > limits.characters {
				return nil, fmt.Errorf("%w: characters exceed %d", ErrOCRBudget, limits.characters)
			}
		}
	}
	font, err := parseTTF(fontTTF)
	if err != nil {
		return nil, fmt.Errorf("%w: font: %v", ErrInvalidOCRInput, err)
	}
	seenRunes := map[rune]bool{}
	var usedRunes []rune
	layouts := make([][]ocrWordLayout, len(pages))
	for pageIndex, page := range pages {
		for wordIndex, word := range page.Words {
			if word.Confidence < opts.MinConfidence {
				continue
			}
			for _, r := range word.Text {
				if _, ok := font.gid(r); !ok {
					return nil, fmt.Errorf("%w: page %d word %d contains unsupported rune %U", ErrInvalidOCRInput, pageIndex+1, wordIndex+1, r)
				}
				if !seenRunes[r] {
					seenRunes[r] = true
					usedRunes = append(usedRunes, r)
				}
			}
			layout, err := ocrTextMatrix(geometries[pageIndex], word, font)
			if err != nil {
				return nil, fmt.Errorf("page %d word %d: %w", pageIndex+1, wordIndex+1, err)
			}
			layouts[pageIndex] = append(layouts[pageIndex], layout)
		}
	}
	for _, pageLayouts := range layouts {
		if len(pageLayouts) > 1 && !seenRunes[' '] {
			if _, ok := font.gid(' '); !ok {
				return nil, fmt.Errorf("%w: font has no space glyph", ErrInvalidOCRInput)
			}
			seenRunes[' '] = true
			usedRunes = append(usedRunes, ' ')
			break
		}
	}
	if len(usedRunes) == 0 {
		if uint64(len(file)) > limits.outputBytes {
			return nil, fmt.Errorf("%w: output bytes exceed %d", ErrOCRBudget, limits.outputBytes)
		}
		return append([]byte(nil), file...), nil
	}
	font.markUsed(usedRunes...)

	var type0Ref Ref
	fontEmbedded := false
	mutate := func(b *builder, pageIndex int, pd Dict, _ map[int]Ref) error {
		if len(layouts[pageIndex]) == 0 {
			return nil
		}
		if !fontEmbedded {
			var err error
			type0Ref, err = embedTTF(b, font, usedRunes)
			if err != nil {
				return err
			}
			fontEmbedded = true
		}
		resources := b.ensureResources(pd)
		fonts := ocrOwnedFonts(b, resources)
		nextFont := 0
		fontName := uniqueResourceName(fonts, "OCR", &nextFont)
		fonts[fontName] = type0Ref

		var content bytes.Buffer
		content.WriteString("q\n")
		for layoutIndex, layout := range layouts[pageIndex] {
			text := layout.word.Text
			if layoutIndex+1 < len(layouts[pageIndex]) {
				text += " "
			}
			fmt.Fprintf(&content,
				"BT /%s %s Tf 3 Tr %s %s %s %s %s %s Tm <%X> Tj ET\n",
				fontName, formatPDFNumber(layout.fontSize),
				formatPDFNumber(layout.a), formatPDFNumber(layout.b), formatPDFNumber(layout.c),
				formatPDFNumber(layout.d), formatPDFNumber(layout.e), formatPDFNumber(layout.f),
				font.encode(text),
			)
		}
		content.WriteString("Q")
		b.appendContent(pd, content.Bytes())
		return nil
	}

	out, err := buildWith([]*Doc{doc}, [][]Page{sourcePages}, mutate)
	if err != nil {
		return nil, err
	}
	if uint64(len(out)) > limits.outputBytes {
		return nil, fmt.Errorf("%w: output bytes exceed %d", ErrOCRBudget, limits.outputBytes)
	}
	return out, nil
}
