//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

const maxOCRPagesJSONBytes = 64 * 1024 * 1024

// pdfRun accepts PDF bytes or an image-byte array and adds an invisible OCR layer.
func run(args []js.Value) any {
	if len(args) != 5 {
		return jsu.Out(nil, fmt.Errorf("OCR PDF expects source, font, pages, source kind, and confidence"))
	}

	pagesJSON := args[2].String()
	if len(pagesJSON) > maxOCRPagesJSONBytes {
		return jsu.Out(nil, fmt.Errorf("%w: OCR pages JSON exceeds %d bytes", pdf.ErrOCRBudget, maxOCRPagesJSONBytes))
	}
	var pages []pdf.OCRPage
	if err := json.Unmarshal([]byte(pagesJSON), &pages); err != nil {
		return jsu.Out(nil, fmt.Errorf("invalid OCR pages: %w", err))
	}

	var source []byte
	var err error
	switch args[3].String() {
	case "pdf":
		source = jsu.Bytes(args[0])
	case "images":
		source, err = pdf.ImagesToPDF(jsu.FileList(args[0]), pdf.ImagePageOpts{AutoRotate: true})
	default:
		err = fmt.Errorf("unsupported OCR source kind")
	}
	if err != nil {
		return jsu.Out(nil, err)
	}

	return jsu.Out(pdf.AddOCRTextLayer(source, jsu.Bytes(args[1]), pages, pdf.OCRLayerOpts{
		MinConfidence: args[4].Float(),
	}))
}

func main() { jsu.Register(run) }
