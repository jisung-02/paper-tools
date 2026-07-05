//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(mdBytes, fontBytes, size) -> PDF bytes.
func run(args []js.Value) any {
	md := jsu.Bytes(args[0])
	fontBytes := jsu.Bytes(args[1])
	size := args[2].Int()
	return jsu.Out(pdf.MarkdownToPDF(md, fontBytes, pdf.MarkdownPDFOpts{FontSize: float64(size)}))
}

func main() { jsu.Register(run) }
