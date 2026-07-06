//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file) -> PDF bytes with annotation appearances flattened.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	return jsu.Out(pdf.Flatten(file))
}

func main() { jsu.Register(run) }
