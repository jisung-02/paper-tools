//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, after, count) -> PDF bytes with blank pages inserted.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	after := args[1].Int()
	count := args[2].Int()
	return jsu.Out(pdf.InsertBlank(file, after, count))
}

func main() { jsu.Register(run) }
