//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, title, author, subject, keywords, strip) -> PDF bytes with
// metadata updated (or entirely stripped). Creator/Producer are left "".
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	title := args[1].String()
	author := args[2].String()
	subject := args[3].String()
	keywords := args[4].String()
	strip := args[5].Bool()
	return jsu.Out(pdf.SetMetadata(file, pdf.DocInfo{
		Title:    title,
		Author:   author,
		Subject:  subject,
		Keywords: keywords,
	}, strip))
}

func main() { jsu.Register(run) }
