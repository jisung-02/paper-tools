package xlsx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// zipEntry is one file to add to a fixture zip.
type zipEntry struct {
	name string
	body string
}

// buildXlsx builds a minimal in-memory .xlsx (zip) package from the given
// entries, always including a generic [Content_Types].xml.
func buildXlsx(entries []zipEntry) []byte {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	all := append([]zipEntry{
		{
			name: "[Content_Types].xml",
			body: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/></Types>`,
		},
	}, entries...)

	for _, e := range all {
		f, err := w.Create(e.name)
		if err != nil {
			panic(err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			panic(err)
		}
	}

	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func workbookXMLFor(sheetNames ...string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, name := range sheetNames {
		sb.WriteString(`<sheet name="`)
		sb.WriteString(name)
		sb.WriteString(`" sheetId="`)
		sb.WriteString(itoa(i + 1))
		sb.WriteString(`" r:id="rId`)
		sb.WriteString(itoa(i + 1))
		sb.WriteString(`"/>`)
	}
	sb.WriteString(`</sheets></workbook>`)
	return sb.String()
}

func relsXMLFor(n int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= n; i++ {
		sb.WriteString(`<Relationship Id="rId`)
		sb.WriteString(itoa(i))
		sb.WriteString(`" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet`)
		sb.WriteString(itoa(i))
		sb.WriteString(`.xml"/>`)
	}
	sb.WriteString(`</Relationships>`)
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

func wrapWorksheet(sheetDataInner string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` + sheetDataInner + `</sheetData></worksheet>`
}

func TestToCSV_SharedStrings(t *testing.T) {
	sst := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>Hello</t></si><si><r><t>Hello </t></r><r><t>World</t></r></si></sst>`
	sheet1 := wrapWorksheet(`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>`)

	data := buildXlsx([]zipEntry{
		{"xl/workbook.xml", workbookXMLFor("Sheet1")},
		{"xl/_rels/workbook.xml.rels", relsXMLFor(1)},
		{"xl/sharedStrings.xml", sst},
		{"xl/worksheets/sheet1.xml", sheet1},
	})

	sheets, err := ToCSV(data)
	if err != nil {
		t.Fatalf("ToCSV returned error: %v", err)
	}
	if len(sheets) != 1 {
		t.Fatalf("expected 1 sheet, got %d", len(sheets))
	}
	want := "Hello,Hello World\n"
	if got := string(sheets[0].CSV); got != want {
		t.Errorf("CSV = %q, want %q", got, want)
	}
}

func TestToCSV_InlineStrings(t *testing.T) {
	sheet1 := wrapWorksheet(`<row r="1"><c r="A1" t="inlineStr"><is><t>Inline text</t></is></c></row>`)

	data := buildXlsx([]zipEntry{
		{"xl/workbook.xml", workbookXMLFor("Sheet1")},
		{"xl/_rels/workbook.xml.rels", relsXMLFor(1)},
		{"xl/worksheets/sheet1.xml", sheet1},
	})

	sheets, err := ToCSV(data)
	if err != nil {
		t.Fatalf("ToCSV returned error: %v", err)
	}
	want := "Inline text\n"
	if got := string(sheets[0].CSV); got != want {
		t.Errorf("CSV = %q, want %q", got, want)
	}
}

func TestToCSV_NumbersAndBooleans(t *testing.T) {
	sheet1 := wrapWorksheet(`<row r="1"><c r="A1"><v>42</v></c><c r="B1"><v>3.14</v></c><c r="C1" t="b"><v>1</v></c><c r="D1" t="b"><v>0</v></c></row>`)

	data := buildXlsx([]zipEntry{
		{"xl/workbook.xml", workbookXMLFor("Sheet1")},
		{"xl/_rels/workbook.xml.rels", relsXMLFor(1)},
		{"xl/worksheets/sheet1.xml", sheet1},
	})

	sheets, err := ToCSV(data)
	if err != nil {
		t.Fatalf("ToCSV returned error: %v", err)
	}
	want := "42,3.14,TRUE,FALSE\n"
	if got := string(sheets[0].CSV); got != want {
		t.Errorf("CSV = %q, want %q", got, want)
	}
}

func TestToCSV_SparseRowsAndColumns(t *testing.T) {
	// Row 1: A1, C1 (B1 empty). Row 2: entirely missing. Row 3: only B3.
	sheet1 := wrapWorksheet(`<row r="1"><c r="A1"><v>1</v></c><c r="C1"><v>3</v></c></row><row r="3"><c r="B3"><v>99</v></c></row>`)

	data := buildXlsx([]zipEntry{
		{"xl/workbook.xml", workbookXMLFor("Sheet1")},
		{"xl/_rels/workbook.xml.rels", relsXMLFor(1)},
		{"xl/worksheets/sheet1.xml", sheet1},
	})

	sheets, err := ToCSV(data)
	if err != nil {
		t.Fatalf("ToCSV returned error: %v", err)
	}
	want := "1,,3\n\n,99\n"
	if got := string(sheets[0].CSV); got != want {
		t.Errorf("CSV = %q, want %q", got, want)
	}
}

func TestToCSV_MultipleSheets(t *testing.T) {
	sheet1 := wrapWorksheet(`<row r="1"><c r="A1"><v>1</v></c></row>`)
	sheet2 := wrapWorksheet(`<row r="1"><c r="A1"><v>2</v></c></row>`)

	data := buildXlsx([]zipEntry{
		{"xl/workbook.xml", workbookXMLFor("First", "Second")},
		{"xl/_rels/workbook.xml.rels", relsXMLFor(2)},
		{"xl/worksheets/sheet1.xml", sheet1},
		{"xl/worksheets/sheet2.xml", sheet2},
	})

	sheets, err := ToCSV(data)
	if err != nil {
		t.Fatalf("ToCSV returned error: %v", err)
	}
	if len(sheets) != 2 {
		t.Fatalf("expected 2 sheets, got %d", len(sheets))
	}
	if sheets[0].Name != "First" || sheets[1].Name != "Second" {
		t.Errorf("sheet names/order = %q, %q; want First, Second", sheets[0].Name, sheets[1].Name)
	}
	if got := string(sheets[0].CSV); got != "1\n" {
		t.Errorf("sheet1 CSV = %q, want %q", got, "1\n")
	}
	if got := string(sheets[1].CSV); got != "2\n" {
		t.Errorf("sheet2 CSV = %q, want %q", got, "2\n")
	}
}

func TestToCSV_NotXlsx(t *testing.T) {
	data := buildXlsx([]zipEntry{
		{"unrelated.txt", "just some file, no workbook.xml here"},
	})

	sheets, err := ToCSV(data)
	if err == nil {
		t.Fatalf("expected error for zip without xl/workbook.xml, got sheets: %v", sheets)
	}
	if len(err.Error()) > 60 {
		t.Errorf("expected a clean/short error message, got %d chars: %q", len(err.Error()), err.Error())
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("expected a single-line error message, got: %q", err.Error())
	}
}

func TestToCSV_InvalidZip(t *testing.T) {
	_, err := ToCSV([]byte("not a zip file at all"))
	if err == nil {
		t.Fatal("expected error for non-zip input")
	}
}
