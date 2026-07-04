//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(a, b, reverseB) -> interleaved PDF bytes.
func run(args []js.Value) any {
	a := jsu.Bytes(args[0])
	b := jsu.Bytes(args[1])
	reverseB := args[2].Bool()
	return jsu.Out(pdf.Interleave(a, b, reverseB))
}

func main() { jsu.Register(run) }
