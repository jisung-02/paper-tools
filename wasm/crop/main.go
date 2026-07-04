//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, ranges, left, bottom, right, top) -> cropped PDF bytes.
// Margins arrive already converted to points; the UI does the mm math.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	ranges := args[1].String()
	left := args[2].Float()
	bottom := args[3].Float()
	right := args[4].Float()
	top := args[5].Float()
	return jsu.Out(pdf.Crop(file, ranges, left, bottom, right, top))
}

func main() { jsu.Register(run) }
