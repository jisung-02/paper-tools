//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, ranges, w, h) -> resized PDF bytes (w/h in points).
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	ranges := args[1].String()
	w := args[2].Float()
	h := args[3].Float()
	return jsu.Out(pdf.Resize(file, ranges, w, h))
}

func main() { jsu.Register(run) }
