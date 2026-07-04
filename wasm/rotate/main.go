//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, ranges, degrees) -> rotated PDF bytes. ranges "" = all pages.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	ranges := args[1].String()
	degrees := args[2].Int()
	return jsu.Out(pdf.Rotate(file, ranges, degrees))
}

func main() { jsu.Register(run) }
