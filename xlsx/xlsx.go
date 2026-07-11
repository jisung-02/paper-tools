// Package xlsx converts .xlsx (OOXML spreadsheet) workbooks to CSV, one CSV
// per worksheet.
package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const maxZipEntryBytes = 64 << 20

type limits struct {
	maxZipEntryBytes int
	maxZipEntries    int
	maxZipTotalBytes uint64
	maxRows          int
	maxColumns       int
	maxCSVBytes      int
}

var parseLimits = limits{
	maxZipEntryBytes: maxZipEntryBytes,
	maxZipEntries:    1_024,
	maxZipTotalBytes: 128 << 20,
	maxRows:          1_048_576,
	maxColumns:       16_384,
	maxCSVBytes:      64 << 20,
}

// Sheet holds one worksheet's name and its CSV-encoded contents.
type Sheet struct {
	Name string
	CSV  []byte
}

// wbSheet is a single <sheet> entry in xl/workbook.xml.
type wbSheet struct {
	Name string `xml:"name,attr"`
	RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
}

// workbookXML is the root of xl/workbook.xml (only the parts we need).
type workbookXML struct {
	Sheets []wbSheet `xml:"sheets>sheet"`
}

// relationship is a single <Relationship> entry in a .rels part.
type relationship struct {
	ID     string `xml:"Id,attr"`
	Target string `xml:"Target,attr"`
}

// relationshipsXML is the root of a .rels part.
type relationshipsXML struct {
	Relationship []relationship `xml:"Relationship"`
}

// siXML is a single shared-string entry (<si>) in xl/sharedStrings.xml.
// If T is set, it's a plain string; otherwise the text is spread across R
// (rich-text run) children and must be concatenated in order.
type siXML struct {
	T *string `xml:"t"`
	R []struct {
		T string `xml:"t"`
	} `xml:"r"`
}

// sstXML is the root of xl/sharedStrings.xml.
type sstXML struct {
	SI []siXML `xml:"si"`
}

// cellXML is a single cell (<c>) inside a worksheet row.
type cellXML struct {
	R  string `xml:"r,attr"`
	T  string `xml:"t,attr"`
	V  string `xml:"v"`
	IS *struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}

// rowXML is a single row (<row>) inside a worksheet's sheetData.
type rowXML struct {
	R     string    `xml:"r,attr"`
	Cells []cellXML `xml:"c"`
}

// sheetDataXML is the <sheetData> element of a worksheet.
type sheetDataXML struct {
	Rows []rowXML `xml:"row"`
}

// worksheetXML is the root of an xl/worksheets/sheetN.xml part.
type worksheetXML struct {
	SheetData sheetDataXML `xml:"sheetData"`
}

// ToCSV parses an .xlsx workbook and converts every worksheet to CSV,
// returning one Sheet per worksheet in workbook order.
func ToCSV(data []byte) ([]Sheet, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("유효한 xlsx 파일이 아닙니다")
	}
	if len(zr.File) > parseLimits.maxZipEntries {
		return nil, errors.New("xlsx 파일에 항목이 너무 많습니다")
	}

	files := make(map[string]*zip.File, len(zr.File))
	var total uint64
	for _, f := range zr.File {
		if f.UncompressedSize64 > uint64(parseLimits.maxZipEntryBytes) {
			return nil, fmt.Errorf("xlsx entry %q too large: limit %d bytes", f.Name, parseLimits.maxZipEntryBytes)
		}
		total += f.UncompressedSize64
		if total > parseLimits.maxZipTotalBytes {
			return nil, errors.New("xlsx 압축 해제 크기가 제한을 초과했습니다")
		}
		files[f.Name] = f
	}

	wbFile, ok := files["xl/workbook.xml"]
	if !ok {
		return nil, errors.New("유효한 xlsx 파일이 아닙니다")
	}
	wbBytes, err := readZipFile(wbFile)
	if err != nil {
		return nil, errors.New("유효한 xlsx 파일이 아닙니다")
	}

	var wb workbookXML
	if err := xml.Unmarshal(wbBytes, &wb); err != nil {
		return nil, errors.New("유효한 xlsx 파일이 아닙니다")
	}
	if len(wb.Sheets) == 0 {
		return nil, errors.New("xlsx 파일에 시트가 없습니다")
	}

	// Resolve rId -> Target from xl/_rels/workbook.xml.rels, if present.
	rels := map[string]string{}
	if relsFile, ok := files["xl/_rels/workbook.xml.rels"]; ok {
		relsBytes, err := readZipFile(relsFile)
		if err != nil {
			return nil, err
		}
		var relsXML relationshipsXML
		if err := xml.Unmarshal(relsBytes, &relsXML); err != nil {
			return nil, fmt.Errorf("유효한 xlsx 관계 파일이 아닙니다: %w", err)
		}
		for _, r := range relsXML.Relationship {
			rels[r.ID] = r.Target
		}
	}

	sharedStrings, err := parseSharedStrings(files)
	if err != nil {
		return nil, err
	}

	var sheets []Sheet
	for i, ws := range wb.Sheets {
		path := resolveWorksheetPath(files, rels, ws.RID, i+1)
		wsFile, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("워크시트를 찾을 수 없습니다: %s", ws.Name)
		}

		wsBytes, err := readZipFile(wsFile)
		if err != nil {
			return nil, fmt.Errorf("워크시트를 읽을 수 없습니다: %s: %w", ws.Name, err)
		}

		var worksheet worksheetXML
		if err := xml.Unmarshal(wsBytes, &worksheet); err != nil {
			return nil, fmt.Errorf("유효한 워크시트가 아닙니다: %s: %w", ws.Name, err)
		}

		csvBytes, err := worksheetToCSV(worksheet, sharedStrings)
		if err != nil {
			return nil, err
		}
		sheets = append(sheets, Sheet{Name: ws.Name, CSV: csvBytes})
	}

	return sheets, nil
}

// readZipFile fully reads a *zip.File's contents.
func readZipFile(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > uint64(parseLimits.maxZipEntryBytes) {
		return nil, fmt.Errorf("xlsx entry %q too large: limit %d bytes", f.Name, parseLimits.maxZipEntryBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	lr := &io.LimitedReader{R: rc, N: int64(parseLimits.maxZipEntryBytes) + 1}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(lr); err != nil {
		return nil, err
	}
	if buf.Len() > parseLimits.maxZipEntryBytes {
		return nil, fmt.Errorf("xlsx entry %q too large: limit %d bytes", f.Name, parseLimits.maxZipEntryBytes)
	}
	return buf.Bytes(), nil
}

// resolveWorksheetPath finds the zip entry path for a sheet's worksheet
// part. It looks up rID in rels to get the Target, resolves that relative
// to the xl/ directory, and falls back to the conventional
// xl/worksheets/sheetN.xml path (N = 1-based sheet position) if the
// relationship is missing or unresolvable.
func resolveWorksheetPath(files map[string]*zip.File, rels map[string]string, rID string, position int) string {
	if target, ok := rels[rID]; ok && target != "" {
		var path string
		switch {
		case strings.HasPrefix(target, "/"):
			path = strings.TrimPrefix(target, "/")
		case strings.HasPrefix(target, "xl/"):
			path = target
		default:
			path = "xl/" + target
		}
		if _, ok := files[path]; ok {
			return path
		}
	}
	return fmt.Sprintf("xl/worksheets/sheet%d.xml", position)
}

// parseSharedStrings parses xl/sharedStrings.xml into an ordered slice of
// resolved strings. A missing file is not an error: it just means there are
// no shared strings (a workbook with no string cells may omit the part).
func parseSharedStrings(files map[string]*zip.File) ([]string, error) {
	f, ok := files["xl/sharedStrings.xml"]
	if !ok {
		return nil, nil
	}
	b, err := readZipFile(f)
	if err != nil {
		return nil, err
	}

	var sst sstXML
	if err := xml.Unmarshal(b, &sst); err != nil {
		return nil, fmt.Errorf("유효한 xlsx 공유 문자열 파일이 아닙니다: %w", err)
	}

	out := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		if si.T != nil {
			out[i] = *si.T
			continue
		}
		var sb strings.Builder
		for _, r := range si.R {
			sb.WriteString(r.T)
		}
		out[i] = sb.String()
	}
	return out, nil
}

// colLetterToIndex converts an Excel column reference (e.g. "A", "Z", "AA")
// to its 1-based column index using bijective base-26 numbering.
func colLetterToIndex(letters string) int {
	idx := 0
	for _, c := range letters {
		idx = idx*26 + int(c-'A') + 1
	}
	return idx
}

// splitCellRef splits a cell reference like "AB12" into its column letters
// ("AB") and 1-based row number (12).
func splitCellRef(ref string) (col string, row int, err error) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		i++
	}
	if i == 0 || i == len(ref) {
		return "", 0, fmt.Errorf("잘못된 셀 참조입니다: %s", ref)
	}
	col = ref[:i]
	row, err = strconv.Atoi(ref[i:])
	if err != nil {
		return "", 0, fmt.Errorf("잘못된 셀 참조입니다: %s", ref)
	}
	return col, row, nil
}

// cellValue resolves a cell's displayed string value according to its "t"
// (type) attribute.
//
// ponytail: Excel stores dates as numeric serial day-counts (e.g. 45000),
// not calendar strings; distinguishing a "date" cell from a plain number
// requires cross-referencing the cell's style/number-format, which this
// package does not parse. Numeric and unspecified-type cells therefore emit
// the raw <v> text verbatim, serial numbers included — this is a documented
// limitation, not a bug.
func cellValue(c cellXML, sharedStrings []string) string {
	switch c.T {
	case "s":
		idx, err := strconv.Atoi(c.V)
		if err != nil || idx < 0 || idx >= len(sharedStrings) {
			return ""
		}
		return sharedStrings[idx]
	case "str":
		return c.V
	case "inlineStr":
		if c.IS == nil {
			return ""
		}
		if c.IS.T != "" {
			return c.IS.T
		}
		var sb strings.Builder
		for _, r := range c.IS.R {
			sb.WriteString(r.T)
		}
		return sb.String()
	case "b":
		if c.V == "1" {
			return "TRUE"
		}
		return "FALSE"
	default:
		return c.V
	}
}

// worksheetToCSV converts a parsed worksheet into CSV bytes, preserving
// blank rows/columns implied by gaps in row/column numbering.
func worksheetToCSV(ws worksheetXML, sharedStrings []string) ([]byte, error) {
	var records [][]string
	prevRowNum := 0

	for _, row := range ws.SheetData.Rows {
		rowNum, err := strconv.Atoi(row.R)
		if err != nil || rowNum <= 0 {
			rowNum = prevRowNum + 1
		}
		if rowNum > parseLimits.maxRows {
			return nil, fmt.Errorf("xlsx row limit exceeded: %d", parseLimits.maxRows)
		}

		for prevRowNum+1 < rowNum {
			records = append(records, []string{})
			prevRowNum++
		}

		type resolvedCell struct {
			col int
			val string
		}
		var cells []resolvedCell
		maxCol := 0
		for _, c := range row.Cells {
			col, _, err := splitCellRef(c.R)
			if err != nil {
				return nil, err
			}
			colIdx := colLetterToIndex(col)
			if colIdx > parseLimits.maxColumns {
				return nil, fmt.Errorf("xlsx column limit exceeded: %d", parseLimits.maxColumns)
			}
			if colIdx > maxCol {
				maxCol = colIdx
			}
			cells = append(cells, resolvedCell{col: colIdx, val: cellValue(c, sharedStrings)})
		}

		record := make([]string, maxCol)
		for _, rc := range cells {
			record[rc.col-1] = rc.val
		}
		records = append(records, record)
		prevRowNum = rowNum
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&limitedBuffer{buf: &buf, remaining: parseLimits.maxCSVBytes})
	for _, rec := range records {
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

type limitedBuffer struct {
	buf       *bytes.Buffer
	remaining int
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, errors.New("xlsx csv output exceeds configured limit")
	}
	n, err := w.buf.Write(p)
	w.remaining -= n
	return n, err
}
