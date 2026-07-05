//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, image, position, widthPercent, opacity, pages) -> PDF bytes
// with the stamp/signature image overlaid on the selected pages.
//
//   - file:         PDF bytes
//   - image:        PNG or JPEG bytes (PNG alpha becomes an /SMask, so
//     transparent stamp/signature backgrounds show through)
//   - position:     one of "top-left", "top-center", "top-right",
//     "middle-left", "center", "middle-right", "bottom-left",
//     "bottom-center", "bottom-right" ("" -> "bottom-right")
//   - widthPercent: stamp width as % of page width, aspect preserved
//     (<=0 -> 20)
//   - opacity:      0-1 via ExtGState ca/CA (<=0 -> 1, fully opaque)
//   - pages:        ParseRanges syntax ("1-3,5"), "first", "last", or ""
//     for all pages
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	img := jsu.Bytes(args[1])
	position := args[2].String()
	widthPercent := args[3].Float()
	opacity := args[4].Float()
	pages := args[5].String()
	return jsu.Out(pdf.StampImage(file, img, pdf.StampOpts{
		Position:     pdf.Position(position),
		WidthPercent: widthPercent,
		Opacity:      opacity,
		Pages:        pages,
	}))
}

func main() { jsu.Register(run) }
