//go:build js && wasm

package main

import (
	"archive/zip"
	"bytes"
	"strings"
	"syscall/js"

	"file-utils/wasm/jsu"
	"file-utils/xlsx"
)

// pdfRun(file) -> single sheet: CSV bytes (name the download "<sheet>.csv").
// Multiple sheets: a ZIP archive with one "<sheet>.csv" entry per sheet
// (name the download "<basename>.zip"); mirrors pdf.ExtractImages' ZIP convention.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	sheets, err := xlsx.ToCSV(file)
	if err != nil {
		return jsu.Out(nil, err)
	}
	if len(sheets) == 1 {
		return jsu.Out(sheets[0].CSV, nil)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, s := range sheets {
		w, err := zw.Create(sanitizeFilename(s.Name) + ".csv")
		if err != nil {
			zw.Close()
			return jsu.Out(nil, err)
		}
		if _, err := w.Write(s.CSV); err != nil {
			zw.Close()
			return jsu.Out(nil, err)
		}
	}
	if err := zw.Close(); err != nil {
		return jsu.Out(nil, err)
	}
	return jsu.Out(buf.Bytes(), nil)
}

// sanitizeFilename replaces characters that are illegal in filenames on
// common filesystems (Windows in particular) so sheet names can be used
// safely as ZIP entry names.
func sanitizeFilename(name string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	s := r.Replace(name)
	if s == "" {
		s = "sheet"
	}
	return s
}

func main() { jsu.Register(run) }
