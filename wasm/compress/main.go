//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, quality, maxWidth, grayscale) -> recompressed PDF bytes.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	quality := args[1].Int()
	maxWidth := args[2].Int()
	grayscale := false
	if len(args) > 3 {
		grayscale = args[3].Bool()
	}
	return jsu.Out(pdf.Compress(file, pdf.CompressOpts{
		JPEGQuality: quality,
		MaxWidth:    maxWidth,
		Grayscale:   grayscale,
	}))
}

func main() { jsu.Register(run) }
