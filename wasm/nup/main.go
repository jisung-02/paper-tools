//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, per) -> PDF bytes with `per` (2 or 4) pages per sheet.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	per := args[1].Int()
	return jsu.Out(pdf.NUp(file, per))
}

func main() { jsu.Register(run) }
