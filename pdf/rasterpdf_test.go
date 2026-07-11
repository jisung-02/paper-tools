package pdf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
)

type failingRasterWriter struct {
	bytes.Buffer
	failAfter int
}

type rasterWriteBoundaryRecorder struct {
	bytes.Buffer
	wholeObjects int
}

func (w *rasterWriteBoundaryRecorder) Write(p []byte) (int, error) {
	if bytes.HasPrefix(p, []byte("3 0 obj\n")) && bytes.Contains(p, []byte("\nendobj\n")) {
		w.wholeObjects++
	}
	return w.Buffer.Write(p)
}

var errRasterWriterSentinel = errors.New("raster writer sentinel")

type rasterSentinelWriter struct {
	bytes.Buffer
	writes int
}

func (w *rasterSentinelWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= 2 {
		return 0, errRasterWriterSentinel
	}
	return w.Buffer.Write(p)
}

func (w *failingRasterWriter) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.failAfter {
		return 0, errors.New("writer stopped")
	}
	return w.Buffer.Write(p)
}

func TestRasterPDFEncoderStreamsPagesAndEnforcesLifecycle(t *testing.T) {
	pages := []RasterPage{
		{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 200},
		{PNGData: testPNG(t, 3, 2), WidthPt: 200, HeightPt: 100},
	}
	var out bytes.Buffer
	encoder, err := NewRasterPDFEncoder(&out, len(pages), RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("constructor did not stream the PDF header")
	}
	for i, page := range pages {
		before := out.Len()
		if err := encoder.AddPage(page); err != nil {
			t.Fatalf("AddPage %d: %v", i+1, err)
		}
		if out.Len() <= before {
			t.Fatalf("AddPage %d wrote no page objects", i+1)
		}
	}
	if err := encoder.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRasterOnlyPDF(out.Bytes(), 2); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Finish(); !errors.Is(err, ErrRasterPDFLifecycle) {
		t.Fatalf("duplicate Finish = %v", err)
	}
	if err := encoder.AddPage(pages[0]); !errors.Is(err, ErrRasterPDFLifecycle) {
		t.Fatalf("Add after Finish = %v", err)
	}
	encoder.Abort()
	encoder.Abort()
}

func TestRasterPDFEncoderPageCountAndPoisoning(t *testing.T) {
	page := RasterPage{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 100}
	var short bytes.Buffer
	encoder, err := NewRasterPDFEncoder(&short, 2, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.AddPage(page); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Finish(); !errors.Is(err, ErrRasterPDFLifecycle) {
		t.Fatalf("short Finish = %v", err)
	}

	writer := &failingRasterWriter{failAfter: 32}
	poisoned, err := NewRasterPDFEncoder(writer, 1, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := poisoned.AddPage(page); err == nil {
		t.Fatal("writer failure was accepted")
	}
	if err := poisoned.AddPage(page); !errors.Is(err, ErrRasterPDFLifecycle) {
		t.Fatalf("Add after poison = %v", err)
	}
	if err := poisoned.Finish(); !errors.Is(err, ErrRasterPDFLifecycle) {
		t.Fatalf("Finish after poison = %v", err)
	}
	poisoned.Abort()
	poisoned.Abort()
}

func TestRasterPDFEncoderWritesObjectPayloadDirectly(t *testing.T) {
	writer := &rasterWriteBoundaryRecorder{}
	encoder, err := NewRasterPDFEncoder(writer, 1, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.AddPage(RasterPage{PNGData: testPNG(t, 64, 64), WidthPt: 100, HeightPt: 100}); err != nil {
		t.Fatal(err)
	}
	if writer.wholeObjects != 0 {
		t.Fatalf("writer received %d full object copies", writer.wholeObjects)
	}
}

func TestRasterPDFEncoderPropagatesFirstWriterError(t *testing.T) {
	writer := &rasterSentinelWriter{}
	encoder, err := NewRasterPDFEncoder(writer, 1, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.AddPage(RasterPage{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 100}); !errors.Is(err, errRasterWriterSentinel) {
		t.Fatalf("AddPage error = %v", err)
	}
	if writer.writes != 2 {
		t.Fatalf("writes after first error = %d", writer.writes)
	}
	if err := encoder.Finish(); !errors.Is(err, ErrRasterPDFLifecycle) {
		t.Fatalf("Finish after writer error = %v", err)
	}
}

func TestBuildRasterOnlyPDFMatchesManualEncoder(t *testing.T) {
	pages := []RasterPage{
		{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 200},
		{PNGData: testPNG(t, 3, 2), WidthPt: 200, HeightPt: 100},
	}
	var manual bytes.Buffer
	encoder, err := NewRasterPDFEncoder(&manual, len(pages), RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		if err := encoder.AddPage(page); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Finish(); err != nil {
		t.Fatal(err)
	}
	built, err := BuildRasterOnlyPDF(pages, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(built, manual.Bytes()) {
		t.Fatal("Build used a different serialization path")
	}
}

func TestRasterPDFEncoderPNGByteBudgets(t *testing.T) {
	page := RasterPage{PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 100}
	for _, tc := range []struct {
		name string
		opts RasterPDFOpts
	}{
		{name: "per page", opts: RasterPDFOpts{MaxPagePNGBytes: uint64(len(page.PNGData) - 1)}},
		{name: "total", opts: RasterPDFOpts{MaxPNGBytes: uint64(len(page.PNGData)*2 - 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			count := 1
			if tc.name == "total" {
				count = 2
			}
			encoder, err := NewRasterPDFEncoder(&out, count, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "total" {
				if err := encoder.AddPage(page); err != nil {
					t.Fatal(err)
				}
			}
			if err := encoder.AddPage(page); !errors.Is(err, ErrRasterPDFBudget) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

var rasterSourceCanaries = []string{
	"REDACT-SOURCE-TEXT",
	"REDACT-LINK-CANARY",
	"REDACT-JS-CANARY",
	"REDACT-ATTACHMENT-CANARY",
	"REDACT-FORM-CANARY",
	"REDACT-XMP-CANARY",
}

func rasterSourceCanaryPDF(t *testing.T) []byte {
	t.Helper()
	b := &builder{}
	catalogRef := b.alloc()
	pagesRef := b.alloc()
	pageRef := b.alloc()
	contentRef := b.alloc()
	fontRef := b.alloc()
	openActionRef := b.alloc()
	javascriptRef := b.alloc()
	namesRef := b.alloc()
	javascriptNamesRef := b.alloc()
	embeddedNamesRef := b.alloc()
	fileSpecRef := b.alloc()
	embeddedFileRef := b.alloc()
	acroFormRef := b.alloc()
	widgetRef := b.alloc()
	linkRef := b.alloc()
	uriActionRef := b.alloc()
	metadataRef := b.alloc()

	b.objs[catalogRef.Num-1] = Dict{
		"Type":       Name("Catalog"),
		"Pages":      pagesRef,
		"OpenAction": openActionRef,
		"Names":      namesRef,
		"AcroForm":   acroFormRef,
		"Metadata":   metadataRef,
	}
	b.objs[pagesRef.Num-1] = Dict{"Type": Name("Pages"), "Kids": Array{pageRef}, "Count": 1}
	b.objs[pageRef.Num-1] = Dict{
		"Type":     Name("Page"),
		"Parent":   pagesRef,
		"MediaBox": Array{0, 0, 200, 100},
		"Resources": Dict{"Font": Dict{
			"F1": fontRef,
		}},
		"Contents": contentRef,
		"Annots":   Array{widgetRef, linkRef},
	}
	content := []byte("BT /F1 12 Tf 20 50 Td (" + rasterSourceCanaries[0] + ") Tj ET")
	b.objs[contentRef.Num-1] = &Stream{Dict: Dict{"Length": len(content)}, Data: content}
	b.objs[fontRef.Num-1] = Dict{"Type": Name("Font"), "Subtype": Name("Type1"), "BaseFont": Name("Helvetica")}
	b.objs[openActionRef.Num-1] = Dict{"S": Name("JavaScript"), "JS": javascriptRef}
	javascript := []byte(rasterSourceCanaries[2])
	b.objs[javascriptRef.Num-1] = &Stream{Dict: Dict{"Length": len(javascript)}, Data: javascript}
	b.objs[namesRef.Num-1] = Dict{"JavaScript": javascriptNamesRef, "EmbeddedFiles": embeddedNamesRef}
	b.objs[javascriptNamesRef.Num-1] = Dict{"Names": Array{String("startup"), openActionRef}}
	b.objs[embeddedNamesRef.Num-1] = Dict{"Names": Array{String("canary.txt"), fileSpecRef}}
	b.objs[fileSpecRef.Num-1] = Dict{
		"Type": Name("Filespec"), "F": String("canary.txt"), "EF": Dict{"F": embeddedFileRef},
	}
	attachment := []byte(rasterSourceCanaries[3])
	b.objs[embeddedFileRef.Num-1] = &Stream{
		Dict: Dict{"Type": Name("EmbeddedFile"), "Length": len(attachment)}, Data: attachment,
	}
	b.objs[acroFormRef.Num-1] = Dict{"Fields": Array{widgetRef}}
	b.objs[widgetRef.Num-1] = Dict{
		"Type": Name("Annot"), "Subtype": Name("Widget"), "FT": Name("Tx"),
		"T": String(rasterSourceCanaries[4]), "Rect": Array{20, 20, 80, 40}, "P": pageRef,
	}
	b.objs[linkRef.Num-1] = Dict{
		"Type": Name("Annot"), "Subtype": Name("Link"), "Rect": Array{100, 20, 180, 40}, "A": uriActionRef,
	}
	b.objs[uriActionRef.Num-1] = Dict{"S": Name("URI"), "URI": String(rasterSourceCanaries[1])}
	metadata := []byte("<x:xmpmeta>" + strings.Join(rasterSourceCanaries, "|") + "</x:xmpmeta>")
	b.objs[metadataRef.Num-1] = &Stream{
		Dict: Dict{"Type": Name("Metadata"), "Subtype": Name("XML"), "Length": len(metadata)}, Data: metadata,
	}
	out, err := b.bytes(catalogRef)
	if err != nil {
		t.Fatalf("build source canary PDF: %v", err)
	}
	return out
}

func TestRasterPDFEncoderDropsActualSourceCanaryGraph(t *testing.T) {
	source := rasterSourceCanaryPDF(t)
	sourceDoc, err := Parse(source)
	if err != nil {
		t.Fatalf("Parse source: %v", err)
	}
	root, ok := sourceDoc.R(sourceDoc.trailer["Root"]).(Dict)
	if !ok {
		t.Fatal("source Catalog is missing")
	}
	for _, key := range []Name{"OpenAction", "Names", "AcroForm", "Metadata"} {
		if sourceDoc.R(root[key]) == nil {
			t.Errorf("source Catalog is missing /%s", key)
		}
	}
	openAction, ok := sourceDoc.R(root["OpenAction"]).(Dict)
	if !ok || sourceDoc.R(openAction["S"]) != Name("JavaScript") {
		t.Fatal("source JavaScript action is missing")
	}
	javascript, ok := sourceDoc.R(openAction["JS"]).(*Stream)
	if !ok || string(javascript.Data) != rasterSourceCanaries[2] {
		t.Fatalf("source JavaScript = %v", openAction["JS"])
	}
	names, ok := sourceDoc.R(root["Names"]).(Dict)
	if !ok {
		t.Fatal("source Names dictionary is missing")
	}
	javascriptTree, ok := sourceDoc.R(names["JavaScript"]).(Dict)
	if !ok {
		t.Fatal("source JavaScript name tree is missing")
	}
	javascriptEntries, ok := sourceDoc.R(javascriptTree["Names"]).(Array)
	var javascriptActionRef Ref
	actionRefOK := false
	if ok && len(javascriptEntries) == 2 {
		javascriptActionRef, actionRefOK = javascriptEntries[1].(Ref)
	}
	openActionRef, openActionRefOK := root["OpenAction"].(Ref)
	if !ok || len(javascriptEntries) != 2 || !actionRefOK || !openActionRefOK || javascriptActionRef != openActionRef {
		t.Fatal("source JavaScript name tree does not reference the action")
	}
	embeddedTree, ok := sourceDoc.R(names["EmbeddedFiles"]).(Dict)
	if !ok {
		t.Fatal("source EmbeddedFiles name tree is missing")
	}
	embeddedEntries, ok := sourceDoc.R(embeddedTree["Names"]).(Array)
	if !ok || len(embeddedEntries) != 2 {
		t.Fatal("source embedded file entry is missing")
	}
	fileSpec, ok := sourceDoc.R(embeddedEntries[1]).(Dict)
	if !ok {
		t.Fatal("source embedded file specification is missing")
	}
	ef, ok := sourceDoc.R(fileSpec["EF"]).(Dict)
	if !ok {
		t.Fatal("source embedded file EF dictionary is missing")
	}
	attachment, ok := sourceDoc.R(ef["F"]).(*Stream)
	if !ok || string(attachment.Data) != rasterSourceCanaries[3] {
		t.Fatalf("source attachment = %v", ef["F"])
	}
	acroForm, ok := sourceDoc.R(root["AcroForm"]).(Dict)
	if !ok {
		t.Fatal("source AcroForm is missing")
	}
	fields, ok := sourceDoc.R(acroForm["Fields"]).(Array)
	if !ok || len(fields) != 1 {
		t.Fatal("source form field is missing")
	}
	widget, ok := sourceDoc.R(fields[0]).(Dict)
	widgetName, widgetNameOK := widget["T"].(String)
	if !ok || widget["Subtype"] != Name("Widget") || !widgetNameOK || string(widgetName) != rasterSourceCanaries[4] {
		t.Fatalf("source widget = %v", widget)
	}
	metadata, ok := sourceDoc.R(root["Metadata"]).(*Stream)
	if !ok || !bytes.Contains(metadata.Data, []byte(rasterSourceCanaries[5])) {
		t.Fatal("source XMP metadata canary is missing")
	}
	sourcePages, err := sourceDoc.Pages()
	if err != nil || len(sourcePages) != 1 {
		t.Fatalf("source Pages = %v, %v", sourcePages, err)
	}
	sourcePage, ok := sourceDoc.Get(sourcePages[0].Num).(Dict)
	if !ok || sourceDoc.R(sourcePage["Annots"]) == nil {
		t.Fatal("source page annotations are missing")
	}
	annotations := sourceDoc.R(sourcePage["Annots"]).(Array)
	var linkCanary string
	for _, annotationRef := range annotations {
		annotation, ok := sourceDoc.R(annotationRef).(Dict)
		if !ok || annotation["Subtype"] != Name("Link") {
			continue
		}
		action, _ := sourceDoc.R(annotation["A"]).(Dict)
		if uri, ok := action["URI"].(String); ok {
			linkCanary = string(uri)
		}
	}
	if linkCanary != rasterSourceCanaries[1] {
		t.Fatalf("source link URI = %q", linkCanary)
	}
	sourceText, err := ExtractText(source)
	if err != nil || !strings.Contains(sourceText, rasterSourceCanaries[0]) {
		t.Fatalf("source text = %q, %v", sourceText, err)
	}
	for _, canary := range rasterSourceCanaries {
		if !bytes.Contains(source, []byte(canary)) {
			t.Errorf("source fixture is missing %q", canary)
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, 3, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	img.SetNRGBA(1, 1, color.NRGBA{A: 255})
	var pagePNG bytes.Buffer
	if err := png.Encode(&pagePNG, img); err != nil {
		t.Fatal(err)
	}
	out, err := BuildRasterOnlyPDF([]RasterPage{{PNGData: pagePNG.Bytes(), WidthPt: 100, HeightPt: 100}}, RasterPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range rasterSourceCanaries {
		if bytes.Contains(out, []byte(canary)) {
			t.Errorf("output retained %q", canary)
		}
	}
	for _, forbidden := range []string{"/Annots", "/OpenAction", "/JavaScript", "/EmbeddedFiles", "/AcroForm", "/Metadata"} {
		if bytes.Contains(out, []byte(forbidden)) {
			t.Errorf("output retained %s", forbidden)
		}
	}
	outText, err := ExtractText(out)
	if err != nil || outText != "" {
		t.Fatalf("output text = %q, %v", outText, err)
	}
	assertRasterOnlyGraph(t, out, 1)
}

func pngWithTextCanary(t *testing.T, canary string) []byte {
	t.Helper()
	base := testPNG(t, 3, 2)
	if len(base) < 12 || string(base[len(base)-8:len(base)-4]) != "IEND" {
		t.Fatal("test PNG has no final IEND chunk")
	}
	data := append([]byte("Comment\x00"), []byte(canary)...)
	chunk := make([]byte, 12+len(data))
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(data)))
	copy(chunk[4:8], "tEXt")
	copy(chunk[8:8+len(data)], data)
	binary.BigEndian.PutUint32(chunk[8+len(data):], crc32.ChecksumIEEE(chunk[4:8+len(data)]))
	out := append([]byte(nil), base[:len(base)-12]...)
	out = append(out, chunk...)
	out = append(out, base[len(base)-12:]...)
	return out
}

func assertExactKeys(t *testing.T, label string, d Dict, allowed ...Name) {
	t.Helper()
	want := make(map[Name]bool, len(allowed))
	for _, key := range allowed {
		want[key] = true
	}
	for key := range d {
		if !want[key] {
			t.Errorf("%s has forbidden key /%s", label, key)
		}
	}
	for key := range want {
		if _, ok := d[key]; !ok {
			t.Errorf("%s is missing /%s", label, key)
		}
	}
}

func assertRasterOnlyGraph(t *testing.T, data []byte, wantPages int) {
	t.Helper()
	d, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	assertExactKeys(t, "trailer", d.trailer, "Root", "Size")
	allowedObjects := map[int]bool{}
	rootRef, ok := d.trailer["Root"].(Ref)
	if !ok {
		t.Fatalf("trailer Root = %v", d.trailer["Root"])
	}
	allowedObjects[rootRef.Num] = true
	root, ok := d.Get(rootRef.Num).(Dict)
	if !ok {
		t.Fatalf("root is not a dict")
	}
	assertExactKeys(t, "Catalog", root, "Type", "Pages")
	if typ, _ := root["Type"].(Name); typ != "Catalog" {
		t.Errorf("Catalog Type = %v", root["Type"])
	}
	pagesRef, ok := root["Pages"].(Ref)
	if !ok {
		t.Fatalf("Catalog Pages = %v", root["Pages"])
	}
	allowedObjects[pagesRef.Num] = true
	pagesRoot, ok := d.Get(pagesRef.Num).(Dict)
	if !ok {
		t.Fatalf("Pages root is not a dict")
	}
	assertExactKeys(t, "Pages", pagesRoot, "Type", "Kids", "Count")
	kids, ok := d.R(pagesRoot["Kids"]).(Array)
	if !ok || len(kids) != wantPages {
		t.Fatalf("Kids = %v, want %d pages", pagesRoot["Kids"], wantPages)
	}
	if count, _ := rnum(pagesRoot["Count"]); int(count) != wantPages {
		t.Errorf("Count = %v, want %d", pagesRoot["Count"], wantPages)
	}

	for i, kid := range kids {
		pageRef, ok := kid.(Ref)
		if !ok {
			t.Fatalf("Kids[%d] = %v, want Ref", i, kid)
		}
		allowedObjects[pageRef.Num] = true
		page, ok := d.Get(pageRef.Num).(Dict)
		if !ok {
			t.Fatalf("page %d is not a dict", i+1)
		}
		assertExactKeys(t, "Page", page, "Type", "Parent", "MediaBox", "Resources", "Contents")
		resources, ok := d.R(page["Resources"]).(Dict)
		if !ok {
			t.Fatalf("page %d Resources = %v", i+1, page["Resources"])
		}
		assertExactKeys(t, "Resources", resources, "XObject")
		xobjects, ok := d.R(resources["XObject"]).(Dict)
		if !ok || len(xobjects) != 1 {
			t.Fatalf("page %d XObject resources = %v", i+1, resources["XObject"])
		}
		imageRef, ok := xobjects["Im0"].(Ref)
		if !ok {
			t.Fatalf("page %d /Im0 = %v", i+1, xobjects["Im0"])
		}
		allowedObjects[imageRef.Num] = true
		imageStream, ok := d.Get(imageRef.Num).(*Stream)
		if !ok {
			t.Fatalf("page %d image is not a stream", i+1)
		}
		assertExactKeys(t, "Image", imageStream.Dict, "Type", "Subtype", "Width", "Height", "BitsPerComponent", "ColorSpace", "Filter", "Length")

		contentRef, ok := page["Contents"].(Ref)
		if !ok {
			t.Fatalf("page %d Contents = %v", i+1, page["Contents"])
		}
		allowedObjects[contentRef.Num] = true
		content, ok := d.Get(contentRef.Num).(*Stream)
		if !ok {
			t.Fatalf("page %d content is not a stream", i+1)
		}
		assertExactKeys(t, "Content", content.Dict, "Length")
	}

	for num, entry := range d.xref {
		if num == 0 || entry.typ == 0 {
			continue
		}
		if !allowedObjects[num] {
			t.Errorf("output contains non-whitelisted object %d: %T", num, d.Get(num))
		}
	}
}

func TestBuildRasterOnlyPDFWhitelistAndPageGeometry(t *testing.T) {
	const canary = "SECRET-CANARY-4d2c49c1"
	firstPNG := pngWithTextCanary(t, canary)
	secondPNG := testPNG(t, 4, 3)
	out, err := BuildRasterOnlyPDF([]RasterPage{
		{PNGData: firstPNG, WidthPt: 321.5, HeightPt: 456.25},
		{PNGData: secondPNG, WidthPt: 842, HeightPt: 595},
	}, RasterPDFOpts{})
	if err != nil {
		t.Fatalf("BuildRasterOnlyPDF: %v", err)
	}
	if bytes.Contains(out, []byte(canary)) {
		t.Fatal("PNG metadata canary leaked into raster-only PDF")
	}
	if bytes.Contains(out, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("source PNG bytes were copied verbatim")
	}
	for _, forbidden := range []string{"/Info", "/Metadata", "/Annots", "/AcroForm", "/XFA", "/EmbeddedFiles", "/JavaScript", "/Outlines", "/StructTreeRoot"} {
		if bytes.Contains(out, []byte(forbidden)) {
			t.Errorf("output contains forbidden structure %s", forbidden)
		}
	}
	assertRasterOnlyGraph(t, out, 2)

	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for num, entry := range d.xref {
		if num == 0 || entry.typ == 0 {
			continue
		}
		stream, ok := d.Get(num).(*Stream)
		if !ok {
			continue
		}
		decoded, err := d.decodeStream(stream)
		if err != nil {
			t.Fatalf("decode stream %d: %v", num, err)
		}
		if bytes.Contains(decoded, []byte(canary)) {
			t.Errorf("decoded stream %d retained PNG metadata canary", num)
		}
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	wantBoxes := [][4]float64{{0, 0, 321.5, 456.25}, {0, 0, 842, 595}}
	for i, page := range pages {
		pd := d.Get(page.Num).(Dict)
		x0, y0, x1, y1, ok := docRect(d, pd["MediaBox"])
		if !ok || [4]float64{x0, y0, x1, y1} != wantBoxes[i] {
			t.Errorf("page %d MediaBox = %v, want %v", i+1, [4]float64{x0, y0, x1, y1}, wantBoxes[i])
		}
		content := d.R(pd["Contents"]).(*Stream)
		want := "q " + formatPDFNumber(wantBoxes[i][2]) + " 0 0 " + formatPDFNumber(wantBoxes[i][3]) + " 0 0 cm /Im0 Do Q"
		if string(content.Data) != want {
			t.Errorf("page %d content = %q, want %q", i+1, content.Data, want)
		}
	}
}

func TestBuildRasterOnlyPDFValidationAndBudgets(t *testing.T) {
	validPNG := testPNG(t, 2, 2)
	validPages := func() []RasterPage {
		return []RasterPage{{PNGData: append([]byte(nil), validPNG...), WidthPt: 100, HeightPt: 200}}
	}
	tests := []struct {
		name  string
		pages func() []RasterPage
		opts  RasterPDFOpts
		want  error
	}{
		{name: "no pages", pages: func() []RasterPage { return nil }, want: ErrInvalidRasterPage},
		{name: "missing PNG", pages: func() []RasterPage { return []RasterPage{{WidthPt: 100, HeightPt: 200}} }, want: ErrInvalidRasterPage},
		{name: "malformed PNG", pages: func() []RasterPage { return []RasterPage{{PNGData: []byte("not png"), WidthPt: 100, HeightPt: 200}} }, want: ErrInvalidRasterPage},
		{name: "zero width", pages: func() []RasterPage { p := validPages(); p[0].WidthPt = 0; return p }, want: ErrInvalidRasterPage},
		{name: "negative height", pages: func() []RasterPage { p := validPages(); p[0].HeightPt = -1; return p }, want: ErrInvalidRasterPage},
		{name: "NaN width", pages: func() []RasterPage { p := validPages(); p[0].WidthPt = math.NaN(); return p }, want: ErrInvalidRasterPage},
		{name: "infinite height", pages: func() []RasterPage { p := validPages(); p[0].HeightPt = math.Inf(1); return p }, want: ErrInvalidRasterPage},
		{name: "page dimension above PDF limit", pages: func() []RasterPage { p := validPages(); p[0].WidthPt = 14401; return p }, want: ErrInvalidRasterPage},
		{name: "page count budget", pages: func() []RasterPage { p := validPages(); return append(p, p[0]) }, opts: RasterPDFOpts{MaxPages: 1}, want: ErrRasterPDFBudget},
		{name: "per-page pixel budget", pages: validPages, opts: RasterPDFOpts{MaxPagePixels: 3}, want: ErrRasterPDFBudget},
		{name: "total pixel budget", pages: func() []RasterPage { p := validPages(); return append(p, p[0]) }, opts: RasterPDFOpts{MaxPixels: 7}, want: ErrRasterPDFBudget},
		{name: "PNG byte budget", pages: validPages, opts: RasterPDFOpts{MaxPNGBytes: uint64(len(validPNG) - 1)}, want: ErrRasterPDFBudget},
		{name: "output byte budget", pages: validPages, opts: RasterPDFOpts{MaxOutputBytes: 10}, want: ErrRasterPDFBudget},
		{name: "negative page budget", pages: validPages, opts: RasterPDFOpts{MaxPages: -1}, want: ErrInvalidRasterPage},
		{name: "budget above hard limit", pages: validPages, opts: RasterPDFOpts{MaxPixels: math.MaxUint64}, want: ErrInvalidRasterPage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildRasterOnlyPDF(tc.pages(), tc.opts)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tc.want)
			}
		})
	}
}

func TestBuildRasterOnlyPDFDoesNotRetainTextOrPDFStructures(t *testing.T) {
	canaries := []string{"source-content-canary", "source-metadata-canary", "source-javascript-canary"}
	pngData := pngWithTextCanary(t, strings.Join(canaries, "|"))
	out, err := BuildRasterOnlyPDF([]RasterPage{{PNGData: pngData, WidthPt: 612, HeightPt: 792}}, RasterPDFOpts{})
	if err != nil {
		t.Fatalf("BuildRasterOnlyPDF: %v", err)
	}
	for _, canary := range canaries {
		if bytes.Contains(out, []byte(canary)) {
			t.Errorf("output retained source-only canary %q", canary)
		}
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if text != "" {
		t.Fatalf("raster-only PDF retained searchable text: %q", text)
	}
}

func TestBuildRasterOnlyPDFFlattensPNGAlpha(t *testing.T) {
	out, err := BuildRasterOnlyPDF([]RasterPage{{
		PNGData: stampPNG(t, 2, 2, true), WidthPt: 20, HeightPt: 20,
	}}, RasterPDFOpts{})
	if err != nil {
		t.Fatalf("BuildRasterOnlyPDF: %v", err)
	}
	assertRasterOnlyGraph(t, out, 1)
	d, page := firstImagePDFPage(t, out)
	resources := d.R(page["Resources"]).(Dict)
	xobjects := d.R(resources["XObject"]).(Dict)
	image := d.R(xobjects["Im0"]).(*Stream)
	if _, has := image.Dict["SMask"]; has {
		t.Fatal("raster-only PDF must flatten alpha so hidden RGB cannot survive behind an SMask")
	}
	rgb, err := d.decodeStream(image)
	if err != nil {
		t.Fatalf("decode image: %v", err)
	}
	if len(rgb) != 12 {
		t.Fatalf("decoded RGB bytes = %d, want 12", len(rgb))
	}
	composite := func(foreground, alpha uint8) byte {
		return byte((uint32(foreground)*uint32(alpha) + 255*uint32(255-alpha) + 127) / 255)
	}
	wantLast := []byte{composite(200, 80), composite(20, 80), composite(20, 80)}
	if !bytes.Equal(rgb[9:12], wantLast) {
		t.Fatalf("flattened last pixel = %v, want %v", rgb[9:12], wantLast)
	}
}

func TestBuildRasterOnlyPDFPreservesOpaqueRedactionPixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 220, G: 180, B: 140, A: 255})
		}
	}
	for y := 1; y <= 2; y++ {
		for x := 1; x <= 2; x++ {
			img.SetNRGBA(x, y, color.NRGBA{A: 255})
		}
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	out, err := BuildRasterOnlyPDF([]RasterPage{{PNGData: pngBytes.Bytes(), WidthPt: 40, HeightPt: 40}}, RasterPDFOpts{})
	if err != nil {
		t.Fatalf("BuildRasterOnlyPDF: %v", err)
	}
	d, page := firstImagePDFPage(t, out)
	resources := d.R(page["Resources"]).(Dict)
	xobjects := d.R(resources["XObject"]).(Dict)
	imageStream := d.R(xobjects["Im0"]).(*Stream)
	rgb, err := d.decodeStream(imageStream)
	if err != nil {
		t.Fatalf("decode image: %v", err)
	}
	for y := 1; y <= 2; y++ {
		for x := 1; x <= 2; x++ {
			offset := (y*4 + x) * 3
			if !bytes.Equal(rgb[offset:offset+3], []byte{0, 0, 0}) {
				t.Errorf("redaction pixel (%d,%d) = %v, want opaque black", x, y, rgb[offset:offset+3])
			}
		}
	}
}

func TestValidateRasterOnlyPDFRejectsTrailingData(t *testing.T) {
	out, err := BuildRasterOnlyPDF([]RasterPage{{
		PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 100,
	}}, RasterPDFOpts{})
	if err != nil {
		t.Fatalf("BuildRasterOnlyPDF: %v", err)
	}
	out = append(out, []byte("\nTRAILING-DATA")...)
	if err := validateRasterOnlyPDF(out, 1); !errors.Is(err, ErrRasterPDFInvariant) {
		t.Fatalf("validateRasterOnlyPDF error = %v, want %v", err, ErrRasterPDFInvariant)
	}
}

func TestValidateRasterImageRejectsLengthAndDecodedSizeMismatch(t *testing.T) {
	build := func(t *testing.T) (*Doc, Ref, *Stream) {
		t.Helper()
		out, err := BuildRasterOnlyPDF([]RasterPage{{
			PNGData: testPNG(t, 2, 2), WidthPt: 100, HeightPt: 100,
		}}, RasterPDFOpts{})
		if err != nil {
			t.Fatalf("BuildRasterOnlyPDF: %v", err)
		}
		d, page := firstImagePDFPage(t, out)
		resources := d.R(page["Resources"]).(Dict)
		xobjects := d.R(resources["XObject"]).(Dict)
		ref := xobjects["Im0"].(Ref)
		return d, ref, d.Get(ref.Num).(*Stream)
	}

	t.Run("declared length", func(t *testing.T) {
		d, ref, stream := build(t)
		stream.Dict["Length"] = len(stream.Data) + 1
		if err := validateRasterImage(d, ref, map[int]bool{}); err == nil {
			t.Fatal("validateRasterImage accepted a mismatched /Length")
		}
	})

	t.Run("decoded RGB size", func(t *testing.T) {
		d, ref, stream := build(t)
		stream.Dict["Width"] = 3
		if err := validateRasterImage(d, ref, map[int]bool{}); err == nil {
			t.Fatal("validateRasterImage accepted dimensions that do not match decoded RGB bytes")
		}
	})
}

func TestBuildRasterOnlyPDFChecksOutputBudgetBeforeFullPNGDecode(t *testing.T) {
	pngData := testPNG(t, 64, 64)
	idat := bytes.Index(pngData, []byte("IDAT"))
	if idat < 0 || idat+8 >= len(pngData) {
		t.Fatal("test PNG has no usable IDAT chunk")
	}
	pngData[idat+8] ^= 0xff
	_, err := BuildRasterOnlyPDF([]RasterPage{{
		PNGData: pngData, WidthPt: 100, HeightPt: 100,
	}}, RasterPDFOpts{MaxOutputBytes: 100})
	if !errors.Is(err, ErrRasterPDFBudget) {
		t.Fatalf("error = %v, want early %v", err, ErrRasterPDFBudget)
	}
}

func TestBuildRasterOnlyPDFRejects16BitPNG(t *testing.T) {
	img := image.NewNRGBA64(image.Rect(0, 0, 2, 2))
	img.SetNRGBA64(0, 0, color.NRGBA64{R: 0x1234, G: 0x5678, B: 0x9abc, A: 0xffff})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	_, err := BuildRasterOnlyPDF([]RasterPage{{
		PNGData: encoded.Bytes(), WidthPt: 100, HeightPt: 100,
	}}, RasterPDFOpts{})
	if !errors.Is(err, ErrInvalidRasterPage) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidRasterPage)
	}
}
