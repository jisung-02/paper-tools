//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(fileBytes, fontBytes) -> PDF bytes.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	fontBytes := jsu.Bytes(args[1])
	return jsu.Out(pdf.HwpToPDF(file, fontBytes, pdf.TextPDFOpts{}))
}

func main() { jsu.Register(run) }
