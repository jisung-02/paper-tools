//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, text, fontSize, opacity, diagonal) -> watermarked PDF bytes.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	text := args[1].String()
	fontSize := args[2].Float()
	opacity := args[3].Float()
	diagonal := args[4].Bool()
	return jsu.Out(pdf.Watermark(file, pdf.WatermarkOpts{
		Text:     text,
		FontSize: fontSize,
		Opacity:  opacity,
		Diagonal: diagonal,
	}))
}

func main() { jsu.Register(run) }
