//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, password) -> decrypted PDF bytes.
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	password := args[1].String()
	return jsu.Out(pdf.Unlock(file, password))
}

func main() { jsu.Register(run) }
