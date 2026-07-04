//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/imgconv"
	"file-utils/wasm/jsu"
)

// pdfRun(file, format, quality) -> converted image bytes.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	format := args[1].String()
	quality := args[2].Int()
	return jsu.Out(imgconv.Convert(file, format, quality))
}

func main() { jsu.Register(run) }
