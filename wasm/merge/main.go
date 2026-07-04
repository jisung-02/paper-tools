//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(files[]) -> merged PDF bytes.
func run(args []js.Value) any {
	files := jsu.FileList(args[0])
	return jsu.Out(pdf.Merge(files))
}

func main() { jsu.Register(run) }
