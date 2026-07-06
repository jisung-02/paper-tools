//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(files[], a4) -> PDF bytes with one image per page.
// pdfRun(files[], opts) also accepts {pageSize, orientation, fit, marginPt, autoRotate}.
func run(args []js.Value) any {
	images := jsu.FileList(args[0])
	opts := pdf.ImagePageOpts{}
	if len(args) > 1 {
		switch args[1].Type() {
		case js.TypeBoolean:
			opts.A4 = args[1].Bool()
		case js.TypeObject:
			opts.PageSize = jsString(args[1], "pageSize")
			opts.Orientation = jsString(args[1], "orientation")
			opts.Fit = jsString(args[1], "fit")
			opts.MarginPt = jsFloat(args[1], "marginPt")
			opts.AutoRotate = jsBool(args[1], "autoRotate")
		}
	}
	return jsu.Out(pdf.ImagesToPDF(images, opts))
}

func jsString(obj js.Value, key string) string {
	v := obj.Get(key)
	if v.Type() == js.TypeString {
		return v.String()
	}
	return ""
}

func jsFloat(obj js.Value, key string) float64 {
	v := obj.Get(key)
	if v.Type() == js.TypeNumber {
		return v.Float()
	}
	return 0
}

func jsBool(obj js.Value, key string) bool {
	v := obj.Get(key)
	return v.Type() == js.TypeBoolean && v.Bool()
}

func main() { jsu.Register(run) }
