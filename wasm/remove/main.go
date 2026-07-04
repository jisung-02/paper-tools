//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, ranges) -> PDF bytes with those pages removed.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	ranges := args[1].String()
	return jsu.Out(pdf.RemovePages(file, ranges))
}

func main() { jsu.Register(run) }
