//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file) -> converted docx bytes.
func run(args []js.Value) any {
	return jsu.Out(pdf.HwpxToDocx(jsu.Bytes(args[0])))
}

func main() { jsu.Register(run) }
