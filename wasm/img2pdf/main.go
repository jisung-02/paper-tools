//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(files[], a4) -> PDF bytes with one image per page.
func run(args []js.Value) any {
	images := jsu.FileList(args[0])
	a4 := args[1].Bool()
	return jsu.Out(pdf.ImagesToPDF(images, pdf.ImagePageOpts{A4: a4}))
}

func main() { jsu.Register(run) }
