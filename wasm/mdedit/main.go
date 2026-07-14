//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(mdBytes) -> HTML body fragment (UTF-8) for live Markdown preview.
func run(args []js.Value) any {
	md := jsu.Bytes(args[0])
	return jsu.Out(pdf.MarkdownToHTML(md), nil)
}

func main() { jsu.Register(run) }
