//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(pdfA, pdfB) -> unified-diff-style text report (UTF-8) comparing
// pdfA's and pdfB's extracted text. The "identical" bool DiffText also
// returns isn't surfaced here: the report itself states plainly when the
// two files' text matches, and jsu.Out only ships a plain byte payload.
func run(args []js.Value) any {
	a := jsu.Bytes(args[0])
	b := jsu.Bytes(args[1])
	report, _, err := pdf.DiffText(a, b)
	return jsu.Out([]byte(report), err)
}

func main() { jsu.Register(run) }
