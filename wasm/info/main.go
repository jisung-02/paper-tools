//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file) -> {"json": "..."} describing the document.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	return jsu.JSONOut(pdf.GetInfo(file))
}

func main() { jsu.Register(run) }
