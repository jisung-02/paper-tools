package xlsx

import (
	"strings"
	"testing"
)

func TestToCSVRejectsSparseCoordinatesBeyondConfiguredLimits(t *testing.T) {
	old := parseLimits
	t.Cleanup(func() { parseLimits = old })
	parseLimits.maxRows = 3
	parseLimits.maxColumns = 3

	for _, body := range []string{
		`<row r="4"><c r="A4"><v>x</v></c></row>`,
		`<row r="1"><c r="D1"><v>x</v></c></row>`,
	} {
		data := buildXlsx([]zipEntry{
			{"xl/workbook.xml", workbookXMLFor("Sheet1")},
			{"xl/worksheets/sheet1.xml", wrapWorksheet(body)},
		})
		if _, err := ToCSV(data); err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("ToCSV(%q) error = %v, want coordinate limit error", body, err)
		}
	}
}

func TestToCSVRejectsMalformedWorksheetInsteadOfSkippingIt(t *testing.T) {
	data := buildXlsx([]zipEntry{
		{"xl/workbook.xml", workbookXMLFor("Sheet1")},
		{"xl/worksheets/sheet1.xml", `<worksheet><sheetData><row>`},
	})
	if _, err := ToCSV(data); err == nil {
		t.Fatal("ToCSV accepted malformed worksheet")
	}
}

func TestToCSVRejectsConfiguredOutputBudget(t *testing.T) {
	old := parseLimits
	t.Cleanup(func() { parseLimits = old })
	parseLimits.maxCSVBytes = 4
	data := buildXlsx([]zipEntry{
		{"xl/workbook.xml", workbookXMLFor("Sheet1")},
		{"xl/worksheets/sheet1.xml", wrapWorksheet(`<row r="1"><c r="A1"><v>hello</v></c></row>`)},
	})
	if _, err := ToCSV(data); err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("ToCSV error = %v, want output budget error", err)
	}
}
