//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, userPw, ownerPw) -> AES-128 encrypted PDF bytes.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	userPw := args[1].String()
	ownerPw := args[2].String()
	return jsu.Out(pdf.Protect(file, userPw, ownerPw))
}

func main() { jsu.Register(run) }
