//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, order) -> PDF bytes with pages in the given order.
// Reordering is just an extraction whose ranges list every page once.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	order := args[1].String()
	return jsu.Out(pdf.Split(file, order))
}

func main() { jsu.Register(run) }
