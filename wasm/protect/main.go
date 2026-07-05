//go:build js && wasm

package main

import (
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

// pdfRun(file, userPw, ownerPw, cipher) -> encrypted PDF bytes. cipher is
// "aes128" (default, also used for "" and any unrecognized value) or
// "aes256".
func run(args []js.Value) any {
	file := jsu.Bytes(args[0])
	userPw := args[1].String()
	ownerPw := args[2].String()
	c := pdf.CipherAES128
	if len(args) > 3 && args[3].String() == "aes256" {
		c = pdf.CipherAES256
	}
	return jsu.Out(pdf.ProtectCipher(file, userPw, ownerPw, c))
}

func main() { jsu.Register(run) }
