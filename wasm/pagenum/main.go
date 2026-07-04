//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, format, fontSize) -> PDF bytes with page numbers stamped.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	format := args[1].String()
	fontSize := args[2].Float()
	return jsu.Out(pdf.AddPageNumbers(file, pdf.PageNumOpts{
		Format:   format,
		FontSize: fontSize,
	}))
}

func main() { jsu.Register(run) }
