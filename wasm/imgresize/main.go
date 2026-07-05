//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/imgconv"
	"file-utils/wasm/jsu"
)

// pdfRun(file, maxW, maxH, quality) -> resized image bytes, re-encoded in
// the input's own format. maxW/maxH are in pixels; 0 means "unconstrained"
// on that axis. quality only applies to JPEG input (defaults to 85 if 0).
// The detected output format isn't surfaced here (jsu.Out only carries
// data+error) — it's the same format the input was decoded as.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	maxW := args[1].Int()
	maxH := args[2].Int()
	quality := args[3].Int()
	data, _, err := imgconv.Resize(file, maxW, maxH, quality)
	return jsu.Out(data, err)
}

func main() { jsu.Register(run) }
