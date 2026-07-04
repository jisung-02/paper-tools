//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file) -> ZIP archive of extracted images.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	return jsu.Out(pdf.ExtractImages(file))
}

func main() { jsu.Register(run) }
