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
//
// The newer mode-aware signatures are:
//
//	pdfRun(file, "image", image, position, widthPercent, opacity, pages)
//	pdfRun(file, "text", text, fontBytes, position, fontSize, opacity, pages)
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	if len(args) > 1 && args[1].Type() == js.TypeString {
		switch args[1].String() {
		case "text":
			text := args[2].String()
			font := jsu.Bytes(args[3])
			position := args[4].String()
			fontSize := args[5].Float()
			opacity := args[6].Float()
			pages := args[7].String()
			if len(font) == 0 {
				font = nil
			}
			return jsu.Out(pdf.StampText(file, pdf.StampTextOpts{
				Text:     text,
				FontTTF:  font,
				Position: pdf.Position(position),
				FontSize: fontSize,
				Opacity:  opacity,
				Pages:    pages,
			}))
		case "image":
			img := jsu.Bytes(args[2])
			position := args[3].String()
			widthPercent := args[4].Float()
			opacity := args[5].Float()
			pages := args[6].String()
			return jsu.Out(pdf.StampImage(file, img, pdf.StampOpts{
				Position:     pdf.Position(position),
				WidthPercent: widthPercent,
				Opacity:      opacity,
				Pages:        pages,
			}))
		}
	}

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
