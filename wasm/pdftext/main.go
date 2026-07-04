//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file) -> extracted text bytes.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	text, err := pdf.ExtractText(file)
	return jsu.Out([]byte(text), err)
}

func main() { jsu.Register(run) }
