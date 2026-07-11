package pdf

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func ocrGeometryPDF(t *testing.T, rotations []int) []byte {
	t.Helper()
	b := &builder{}
	catalogRef := b.alloc()
	pagesRef := b.alloc()
	legacyFontRef := b.alloc()
	resourcesRef := b.alloc()
	b.objs[legacyFontRef.Num-1] = Dict{
		"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica"),
	}
	b.objs[resourcesRef.Num-1] = Dict{"Font": Dict{"OCR0": legacyFontRef}}

	kids := make(Array, 0, len(rotations))
	for _, rotation := range rotations {
		pageRef := b.alloc()
		page := Dict{
			"Type":      Name("Page"),
			"Parent":    pagesRef,
			"MediaBox":  Array{10, 20, 210, 120},
			"CropBox":   Array{20, 30, 180, 110},
			"Resources": resourcesRef,
		}
		if rotation != 0 {
			page["Rotate"] = rotation
		}
		b.objs[pageRef.Num-1] = page
		kids = append(kids, pageRef)
	}
	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": kids, "Count": len(kids)}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	out, err := b.bytes(catalogRef)
	if err != nil {
		t.Fatalf("build OCR geometry fixture: %v", err)
	}
	return out
}

func TestAddOCRTextLayerRoundTripGeometryAndResources(t *testing.T) {
	fontBytes := testFont(t)
	font, err := parseTTF(fontBytes)
	if err != nil {
		t.Fatalf("parseTTF: %v", err)
	}
	source := ocrGeometryPDF(t, []int{0, 90, 180, 270})
	words := []string{"Hello 한글", "Rotate ninety", "회전 180", "마지막 270"}
	ocrPages := make([]OCRPage, len(words))
	for i, text := range words {
		ocrPages[i].Words = []OCRWord{
			{Text: text, Left: .1, Top: .2, Right: .6, Bottom: .4, Confidence: .95},
			{Text: "FILTERED", Left: .1, Top: .5, Right: .4, Bottom: .6, Confidence: .1},
		}
	}

	out, err := AddOCRTextLayer(source, fontBytes, ocrPages, OCRLayerOpts{MinConfidence: .5})
	if err != nil {
		t.Fatalf("AddOCRTextLayer: %v", err)
	}
	extracted, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	for _, want := range words {
		if !strings.Contains(extracted, want) {
			t.Errorf("extracted text missing %q: %q", want, extracted)
		}
	}
	if strings.Contains(extracted, "FILTERED") {
		t.Fatalf("low-confidence word survived filtering: %q", extracted)
	}

	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 4 {
		t.Fatalf("pages = %d, want 4", len(pages))
	}

	var sharedFont Ref
	type0Count := 0
	for num, entry := range d.xref {
		if num == 0 || entry.typ == 0 {
			continue
		}
		if dict, ok := d.Get(num).(Dict); ok {
			if subtype, _ := d.R(dict["Subtype"]).(Name); subtype == "Type0" {
				type0Count++
			}
		}
	}

	rotations := []int{0, 90, 180, 270}
	for i, page := range pages {
		pd := d.Get(page.Num).(Dict)
		res, ok := d.R(pd["Resources"]).(Dict)
		if !ok {
			t.Fatalf("page %d Resources is not a dict", i+1)
		}
		fonts, ok := d.R(res["Font"]).(Dict)
		if !ok {
			t.Fatalf("page %d Font resources is not a dict", i+1)
		}
		if len(fonts) != 2 {
			t.Fatalf("page %d Font resources = %v, want only existing /OCR0 and one overlay font", i+1, fonts)
		}
		if _, ok := fonts["OCR0"]; !ok {
			t.Fatalf("page %d lost pre-existing /OCR0 resource", i+1)
		}
		ocrFont, ok := fonts["OCR1"].(Ref)
		if !ok {
			t.Fatalf("page %d missing collision-free /OCR1 font: %v", i+1, fonts)
		}
		if i == 0 {
			sharedFont = ocrFont
		} else if ocrFont != sharedFont {
			t.Fatalf("page %d OCR font = %v, want shared %v", i+1, ocrFont, sharedFont)
		}

		content, ok := lastContentData(t, d, pd)
		if !ok {
			t.Fatalf("page %d has no OCR content", i+1)
		}
		if !bytes.Contains(content, []byte("3 Tr")) {
			t.Fatalf("page %d OCR text is not invisible: %q", i+1, content)
		}

		visualW, visualH := 160.0, 80.0
		if rotations[i] == 90 || rotations[i] == 270 {
			visualW, visualH = visualH, visualW
		}
		boxW, boxH := visualW*.5, visualH*.2
		fontSize := boxH * float64(font.unitsPerEm) / float64(font.ascender-font.descender)
		naturalW := lineWidth(font, []rune(words[i]), fontSize)
		sx := boxW / naturalW
		baselineOffset := -float64(font.descender) * fontSize / float64(font.unitsPerEm)
		u, v := visualW*.1, visualH*.6+baselineOffset
		var a, bb, c, dd, e, f float64
		switch rotations[i] {
		case 90:
			a, bb, c, dd, e, f = 0, sx, -1, 0, 180-v, 30+u
		case 180:
			a, bb, c, dd, e, f = -sx, 0, 0, -1, 180-u, 110-v
		case 270:
			a, bb, c, dd, e, f = 0, -sx, 1, 0, 20+v, 110-u
		default:
			a, bb, c, dd, e, f = sx, 0, 0, 1, 20+u, 30+v
		}
		wantMatrix := strings.Join([]string{formatPDFNumber(a), formatPDFNumber(bb), formatPDFNumber(c), formatPDFNumber(dd), formatPDFNumber(e), formatPDFNumber(f), "Tm"}, " ")
		if !bytes.Contains(content, []byte(wantMatrix)) {
			t.Errorf("page %d content missing matrix %q: %q", i+1, wantMatrix, content)
		}
	}
	if type0Count != 1 {
		t.Fatalf("Type0 font objects = %d, want one shared subset font", type0Count)
	}
}

func TestAddOCRTextLayerPreservesSourceImageBytes(t *testing.T) {
	source, err := ImagesToPDF([][]byte{testPNG(t, 7, 5)}, ImagePageOpts{})
	if err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}
	sourceDoc, sourcePage := firstImagePDFPage(t, source)
	sourceRes := sourceDoc.R(sourcePage["Resources"]).(Dict)
	sourceImages := sourceDoc.R(sourceRes["XObject"]).(Dict)
	sourceImage := sourceDoc.R(sourceImages["Im0"]).(*Stream)

	out, err := AddOCRTextLayer(source, testFont(t), []OCRPage{{Words: []OCRWord{{
		Text: "pixel stable", Left: .1, Top: .1, Right: .9, Bottom: .3, Confidence: .99,
	}}}}, OCRLayerOpts{})
	if err != nil {
		t.Fatalf("AddOCRTextLayer: %v", err)
	}
	outDoc, outPage := firstImagePDFPage(t, out)
	outRes := outDoc.R(outPage["Resources"]).(Dict)
	outImages := outDoc.R(outRes["XObject"]).(Dict)
	outImage := outDoc.R(outImages["Im0"]).(*Stream)
	if !bytes.Equal(outImage.Data, sourceImage.Data) {
		t.Fatal("source image stream changed while adding invisible OCR text")
	}
}

func ocrInheritedImagePDF(t *testing.T) []byte {
	t.Helper()
	b := &builder{}
	catalogRef := b.alloc()
	pagesRef := b.alloc()
	resourcesRef := b.alloc()
	imageRef := b.alloc()
	contentRef := b.alloc()
	pageRef := b.alloc()
	pixel := []byte{17, 34, 51}
	b.objs[imageRef.Num-1] = &Stream{Dict: Dict{
		"Type": Name("XObject"), "Subtype": Name("Image"), "Width": 1, "Height": 1,
		"BitsPerComponent": 8, "ColorSpace": Name("DeviceRGB"), "Length": len(pixel),
	}, Data: pixel}
	b.objs[resourcesRef.Num-1] = Dict{"XObject": Dict{"Im0": imageRef}}
	content := []byte("q 100 0 0 100 0 0 cm /Im0 Do Q")
	b.objs[contentRef.Num-1] = &Stream{Dict: Dict{"Length": len(content)}, Data: content}
	b.objs[pageRef.Num-1] = Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": contentRef}
	b.objs[pagesRef.Num-1] = Dict{
		"Type": Name("Pages"), "Kids": Array{pageRef}, "Count": 1,
		"MediaBox": Array{0, 0, 100, 100}, "Resources": resourcesRef,
	}
	b.objs[catalogRef.Num-1] = Dict{"Type": Name("Catalog"), "Pages": pagesRef}
	out, err := b.bytes(catalogRef)
	if err != nil {
		t.Fatalf("build inherited resource fixture: %v", err)
	}
	return out
}

func TestAddOCRTextLayerPreservesInheritedImageResources(t *testing.T) {
	source := ocrInheritedImagePDF(t)
	out, err := AddOCRTextLayer(source, testFont(t), []OCRPage{{Words: []OCRWord{{
		Text: "inherited", Left: .1, Top: .1, Right: .8, Bottom: .3, Confidence: .99,
	}}}}, OCRLayerOpts{})
	if err != nil {
		t.Fatalf("AddOCRTextLayer: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	pd := d.Get(pages[0].Num).(Dict)
	resources, ok := d.R(pd["Resources"]).(Dict)
	if !ok {
		t.Fatalf("inherited Resources were lost: %v", pd["Resources"])
	}
	xobjects, ok := d.R(resources["XObject"]).(Dict)
	if !ok {
		t.Fatalf("inherited XObject dictionary was lost: %v", resources)
	}
	image, ok := d.R(xobjects["Im0"]).(*Stream)
	if !ok {
		t.Fatalf("inherited /Im0 was lost: %v", xobjects)
	}
	if !bytes.Equal(image.Data, []byte{17, 34, 51}) {
		t.Fatalf("inherited image bytes = %v, want [17 34 51]", image.Data)
	}
}

func TestAddOCRTextLayerValidationAndBudgets(t *testing.T) {
	source := classicPDF()
	font := testFont(t)
	valid := []OCRPage{
		{Words: []OCRWord{{Text: "Hello", Left: .1, Top: .1, Right: .5, Bottom: .2, Confidence: .9}}},
		{Words: []OCRWord{{Text: "한글", Left: .2, Top: .2, Right: .7, Bottom: .4, Confidence: .8}}},
	}
	clone := func() []OCRPage {
		pages := make([]OCRPage, len(valid))
		for i := range valid {
			pages[i].Words = append([]OCRWord(nil), valid[i].Words...)
		}
		return pages
	}

	tests := []struct {
		name  string
		file  []byte
		font  []byte
		pages func() []OCRPage
		opts  OCRLayerOpts
		want  error
	}{
		{name: "invalid PDF", file: []byte("not pdf"), font: font, pages: clone, want: ErrInvalidOCRInput},
		{name: "invalid font", file: source, font: []byte("not ttf"), pages: clone, want: ErrInvalidOCRInput},
		{name: "page count mismatch", file: source, font: font, pages: func() []OCRPage { return clone()[:1] }, want: ErrInvalidOCRInput},
		{name: "NaN coordinate", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Left = math.NaN(); return p }, want: ErrInvalidOCRInput},
		{name: "infinite coordinate", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Top = math.Inf(1); return p }, want: ErrInvalidOCRInput},
		{name: "outside coordinate", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Right = 1.01; return p }, want: ErrInvalidOCRInput},
		{name: "inverted horizontal box", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Right = p[0].Words[0].Left; return p }, want: ErrInvalidOCRInput},
		{name: "inverted vertical box", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Bottom = p[0].Words[0].Top; return p }, want: ErrInvalidOCRInput},
		{name: "invalid confidence", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Confidence = 1.01; return p }, want: ErrInvalidOCRInput},
		{name: "NaN confidence", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Confidence = math.NaN(); return p }, want: ErrInvalidOCRInput},
		{name: "empty text", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Text = " \t"; return p }, want: ErrInvalidOCRInput},
		{name: "invalid UTF-8", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Text = string([]byte{0xff}); return p }, want: ErrInvalidOCRInput},
		{name: "unsupported rune", file: source, font: font, pages: func() []OCRPage { p := clone(); p[0].Words[0].Text = "emoji 😀"; return p }, want: ErrInvalidOCRInput},
		{name: "invalid minimum confidence", file: source, font: font, pages: clone, opts: OCRLayerOpts{MinConfidence: 1.01}, want: ErrInvalidOCRInput},
		{name: "page budget", file: source, font: font, pages: clone, opts: OCRLayerOpts{MaxPages: 1}, want: ErrOCRBudget},
		{name: "word budget counts filtered words", file: source, font: font, pages: func() []OCRPage {
			p := clone()
			p[0].Words = append(p[0].Words, OCRWord{Text: "low", Left: .1, Top: .3, Right: .2, Bottom: .4, Confidence: .01})
			return p
		}, opts: OCRLayerOpts{MinConfidence: .5, MaxWords: 2}, want: ErrOCRBudget},
		{name: "character budget", file: source, font: font, pages: clone, opts: OCRLayerOpts{MaxCharacters: 6}, want: ErrOCRBudget},
		{name: "negative budget", file: source, font: font, pages: clone, opts: OCRLayerOpts{MaxWords: -1}, want: ErrInvalidOCRInput},
		{name: "budget above hard limit", file: source, font: font, pages: clone, opts: OCRLayerOpts{MaxPages: 1 << 30}, want: ErrInvalidOCRInput},
		{name: "input byte budget", file: source, font: font, pages: clone, opts: OCRLayerOpts{MaxInputBytes: 1}, want: ErrOCRBudget},
		{name: "font byte budget", file: source, font: font, pages: clone, opts: OCRLayerOpts{MaxFontBytes: 1}, want: ErrOCRBudget},
		{name: "output byte budget", file: source, font: font, pages: clone, opts: OCRLayerOpts{MaxOutputBytes: 1}, want: ErrOCRBudget},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AddOCRTextLayer(tc.file, tc.font, tc.pages(), tc.opts)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tc.want)
			}
		})
	}
}

func TestAddOCRTextLayerRejectsUnsupportedPageRotation(t *testing.T) {
	_, err := AddOCRTextLayer(
		ocrGeometryPDF(t, []int{45}),
		testFont(t),
		[]OCRPage{{Words: []OCRWord{{Text: "angled", Left: .1, Top: .1, Right: .5, Bottom: .2, Confidence: .9}}}},
		OCRLayerOpts{},
	)
	if !errors.Is(err, ErrInvalidOCRInput) {
		t.Fatalf("error = %v, want ErrInvalidOCRInput", err)
	}
}

func TestAddOCRTextLayerReturnsExactCopyWhenEverythingIsFiltered(t *testing.T) {
	source := classicPDF()
	out, err := AddOCRTextLayer(source, testFont(t), []OCRPage{
		{Words: []OCRWord{{Text: "low", Left: .1, Top: .1, Right: .2, Bottom: .2, Confidence: .1}}},
		{},
	}, OCRLayerOpts{MinConfidence: .5})
	if err != nil {
		t.Fatalf("AddOCRTextLayer: %v", err)
	}
	if !bytes.Equal(out, source) {
		t.Fatal("fully filtered OCR should not rewrite or enlarge the source PDF")
	}
	if len(out) > 0 {
		out[0] ^= 0xff
		if bytes.Equal(out, source) {
			t.Fatal("returned bytes alias the caller's source slice")
		}
	}
}

func TestAddOCRTextLayerPreservesWordBoundaries(t *testing.T) {
	out, err := AddOCRTextLayer(classicPDF(), testFont(t), []OCRPage{
		{Words: []OCRWord{
			{Text: "Hello", Left: .1, Top: .1, Right: .3, Bottom: .2, Confidence: .9},
			{Text: "world", Left: .35, Top: .1, Right: .6, Bottom: .2, Confidence: .9},
		}},
		{},
	}, OCRLayerOpts{})
	if err != nil {
		t.Fatalf("AddOCRTextLayer: %v", err)
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(text, "Hello world") {
		t.Fatalf("extracted text lost word boundary: %q", text)
	}
}

func TestOCRTextMatrixFitsVerticalFontMetrics(t *testing.T) {
	font, err := parseTTF(testFont(t))
	if err != nil {
		t.Fatalf("parseTTF: %v", err)
	}
	g := pageVisualGeometry{x0: 0, y0: 0, x1: 100, y1: 100, width: 100, height: 100}
	word := OCRWord{Text: "Hello", Left: .1, Top: .1, Right: .6, Bottom: .3, Confidence: 1}
	layout, err := ocrTextMatrix(g, word, font)
	if err != nil {
		t.Fatalf("ocrTextMatrix: %v", err)
	}
	wantSize := 20 * float64(font.unitsPerEm) / float64(font.ascender-font.descender)
	wantBaseline := 70 - float64(font.descender)*wantSize/float64(font.unitsPerEm)
	if math.Abs(layout.fontSize-wantSize) > 1e-9 || math.Abs(layout.f-wantBaseline) > 1e-9 {
		t.Fatalf("fontSize/baseline = %g/%g, want %g/%g", layout.fontSize, layout.f, wantSize, wantBaseline)
	}
}

func TestAddOCRTextLayerDoesNotRoundSmallPositiveGeometryToZero(t *testing.T) {
	out, err := AddOCRTextLayer(classicPDF(), testFont(t), []OCRPage{
		{Words: []OCRWord{{Text: "x", Left: .1, Top: .1, Right: .1000000001, Bottom: .1000000001, Confidence: 1}}},
		{},
	}, OCRLayerOpts{})
	if err != nil {
		t.Fatalf("AddOCRTextLayer: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p, _ := d.Pages()
	pd := d.Get(p[0].Num).(Dict)
	data, ok := lastContentData(t, d, pd)
	if !ok {
		t.Fatal("missing OCR content")
	}
	if bytes.Contains(data, []byte(" 0.000000 Tf")) {
		t.Fatalf("small positive font size was rounded to zero: %s", data)
	}
}
