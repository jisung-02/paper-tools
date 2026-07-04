//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(text, fontBytes, size) -> PDF bytes.
func run(args []js.Value) any {
	text := args[0].String()
	fontBytes := jsu.Bytes(args[1])
	size := args[2].Int()
	return jsu.Out(pdf.TextToPDF(text, fontBytes, pdf.TextPDFOpts{FontSize: float64(size)}))
}

func main() { jsu.Register(run) }
