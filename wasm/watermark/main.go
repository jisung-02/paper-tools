//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, text, fontSize, opacity, diagonal, font?) -> watermarked PDF
// bytes. font is an optional Uint8Array of TrueType font bytes; when omitted
// or empty, falls back to the base-14 Helvetica font (Latin-1 text only).
// The font argument is optional so callers built before it was added (e.g.
// existing localized watermark pages) keep working unchanged.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	text := args[1].String()
	fontSize := args[2].Float()
	opacity := args[3].Float()
	diagonal := args[4].Bool()
	var font []byte
	if len(args) > 5 {
		font = jsu.Bytes(args[5])
	}
	if len(font) == 0 {
		font = nil
	}
	return jsu.Out(pdf.Watermark(file, pdf.WatermarkOpts{
		Text:     text,
		FontTTF:  font,
		FontSize: fontSize,
		Opacity:  opacity,
		Diagonal: diagonal,
	}))
}

func main() { jsu.Register(run) }
