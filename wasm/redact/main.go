//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"syscall/js"

	"file-utils/pdf"
	"file-utils/wasm/jsu"
)

var sessions redactSessionManager

func number(obj js.Value, key string) (float64, error) {
	value := obj.Get(key)
	if value.Type() != js.TypeNumber {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	n := value.Float()
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%s must be finite", key)
	}
	return n, nil
}

func optionalUint(obj js.Value, key string) (uint64, error) {
	value := obj.Get(key)
	if value.Type() == js.TypeUndefined || value.Type() == js.TypeNull {
		return 0, nil
	}
	n, err := number(obj, key)
	if err != nil {
		return 0, err
	}
	if n < 0 || n != math.Trunc(n) || n > (1<<53)-1 {
		return 0, fmt.Errorf("%s must be a non-negative safe integer", key)
	}
	return uint64(n), nil
}

func requiredUint(obj js.Value, key string) (uint64, error) {
	value := obj.Get(key)
	if value.Type() == js.TypeUndefined || value.Type() == js.TypeNull {
		return 0, fmt.Errorf("%s is required", key)
	}
	return optionalUint(obj, key)
}

func requestRevision(request js.Value, primary, fallback string) (uint64, error) {
	if value := request.Get(primary); value.Type() != js.TypeUndefined && value.Type() != js.TypeNull {
		return requiredUint(request, primary)
	}
	if fallback != "" {
		return requiredUint(request, fallback)
	}
	return 0, fmt.Errorf("%s is required", primary)
}

func options(value js.Value) (pdf.RasterPDFOpts, error) {
	opts := pdf.RasterPDFOpts{}
	if value.Type() == js.TypeUndefined || value.Type() == js.TypeNull {
		return opts, nil
	}
	if value.Type() != js.TypeObject {
		return opts, fmt.Errorf("redaction options must be an object")
	}
	maxPages, err := optionalUint(value, "maxPages")
	if err != nil || maxPages > (1<<31)-1 {
		if err == nil {
			err = fmt.Errorf("maxPages is too large")
		}
		return opts, err
	}
	opts.MaxPages = int(maxPages)
	fields := []struct {
		name string
		out  *uint64
	}{
		{name: "maxPagePixels", out: &opts.MaxPagePixels},
		{name: "maxPixels", out: &opts.MaxPixels},
		{name: "maxPagePNGBytes", out: &opts.MaxPagePNGBytes},
		{name: "maxPNGBytes", out: &opts.MaxPNGBytes},
		{name: "maxOutputBytes", out: &opts.MaxOutputBytes},
	}
	for _, field := range fields {
		parsed, err := optionalUint(value, field.name)
		if err != nil {
			return opts, err
		}
		*field.out = parsed
	}
	return opts, nil
}

func typedArray(value js.Value, label string) (js.Value, int, error) {
	arrayBuffer := js.Global().Get("ArrayBuffer")
	if value.Type() != js.TypeObject || arrayBuffer.Type() != js.TypeFunction || !arrayBuffer.Call("isView", value).Bool() {
		return js.Value{}, 0, fmt.Errorf("%s must be a typed array", label)
	}
	uint8Array := js.Global().Get("Uint8Array")
	uint8ClampedArray := js.Global().Get("Uint8ClampedArray")
	if uint8Array.Type() != js.TypeFunction || uint8ClampedArray.Type() != js.TypeFunction ||
		(!value.InstanceOf(uint8Array) && !value.InstanceOf(uint8ClampedArray)) {
		return js.Value{}, 0, fmt.Errorf("%s must be a Uint8Array or Uint8ClampedArray", label)
	}
	lengthValue := value.Get("byteLength")
	if lengthValue.Type() != js.TypeNumber {
		return js.Value{}, 0, fmt.Errorf("%s byteLength is invalid", label)
	}
	length := lengthValue.Int()
	if length < 0 {
		return js.Value{}, 0, fmt.Errorf("%s byteLength is invalid", label)
	}
	return value, length, nil
}

func page(value js.Value) (pdf.RasterPage, error) {
	if value.Type() != js.TypeObject {
		return pdf.RasterPage{}, fmt.Errorf("redaction page must be an object")
	}
	pngData, size, err := typedArray(value.Get("pngData"), "redaction page pngData")
	if err != nil {
		return pdf.RasterPage{}, err
	}
	width, err := number(value, "widthPt")
	if err != nil {
		return pdf.RasterPage{}, err
	}
	height, err := number(value, "heightPt")
	if err != nil {
		return pdf.RasterPage{}, err
	}
	data := make([]byte, size)
	if copied := js.CopyBytesToGo(data, pngData); copied != size {
		return pdf.RasterPage{}, fmt.Errorf("redaction page copy is incomplete")
	}
	sessions.RecordPageBridgeCopy(size)
	return pdf.RasterPage{PNGData: data, WidthPt: width, HeightPt: height}, nil
}

func dataJSONOut(data js.Value, value any, err error) any {
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"data": data, "json": string(encoded)}
}

func pageDeclaration(request js.Value) js.Value {
	if value := request.Get("page"); value.Type() == js.TypeObject {
		return value
	}
	return request
}

func command(request js.Value) any {
	commandValue := request.Get("command")
	if commandValue.Type() != js.TypeString {
		return jsu.JSONOut(nil, fmt.Errorf("redaction command must be a string"))
	}
	switch commandValue.String() {
	case "start":
		// Abort before parsing any new field so malformed restarts cannot leave
		// a prior encoder or output available to later commands.
		sessions.Abort()
		pageCount, err := number(request, "pageCount")
		if err != nil || pageCount <= 0 || pageCount != math.Trunc(pageCount) || pageCount > (1<<31)-1 {
			if err == nil {
				err = fmt.Errorf("pageCount must be a positive integer")
			}
			return jsu.JSONOut(nil, err)
		}
		opts, err := options(request.Get("opts"))
		if err != nil {
			sessions.Abort()
			return jsu.JSONOut(nil, err)
		}
		info, err := sessions.Start(int(pageCount), opts)
		return jsu.JSONOut(map[string]any{
			"state": "started", "revision": info.Revision, "maxChunkBytes": info.MaxChunkBytes,
		}, err)
	case "pageStart":
		revision, err := requiredUint(request, "revision")
		declaration := pageDeclaration(request)
		var size uint64
		var width, height float64
		if err == nil {
			size, err = requiredUint(declaration, "size")
		}
		if err == nil {
			width, err = number(declaration, "widthPt")
		}
		if err == nil {
			height, err = number(declaration, "heightPt")
		}
		if err == nil {
			format := declaration.Get("format")
			if format.Type() != js.TypeUndefined && format.Type() != js.TypeNull &&
				(format.Type() != js.TypeString || format.String() != "image/png") {
				err = fmt.Errorf("page format must be image/png")
			}
		}
		if err != nil {
			sessions.Abort()
			return jsu.JSONOut(nil, err)
		}
		info, err := sessions.PageStart(revision, size, width, height)
		return jsu.JSONOut(map[string]any{
			"state": "receiving", "revision": info.Revision, "pageRevision": info.PageRevision,
			"nextOffset": info.NextOffset, "maxChunkBytes": info.MaxChunkBytes,
		}, err)
	case "pageChunk":
		pageRevision, err := requestRevision(request, "pageRevision", "revision")
		var offset uint64
		var data js.Value
		var size int
		if err == nil {
			offset, err = requiredUint(request, "offset")
		}
		if err == nil {
			data, size, err = typedArray(request.Get("data"), "page chunk data")
		}
		if err != nil {
			sessions.Abort()
			return jsu.JSONOut(nil, err)
		}
		target, err := sessions.PageChunkTarget(pageRevision, offset, size)
		if err != nil {
			return jsu.JSONOut(nil, err)
		}
		copied := js.CopyBytesToGo(target, data)
		if copied != size {
			sessions.Abort()
			return jsu.JSONOut(nil, fmt.Errorf("page chunk copy is incomplete"))
		}
		received, err := sessions.CompletePageChunk(pageRevision, copied)
		return jsu.JSONOut(map[string]any{
			"state": "receiving", "pageRevision": pageRevision, "received": received, "nextOffset": received,
		}, err)
	case "pageFinish":
		pageRevision, err := requestRevision(request, "pageRevision", "revision")
		if err != nil {
			sessions.Abort()
			return jsu.JSONOut(nil, err)
		}
		pages, err := sessions.PageFinish(pageRevision)
		return jsu.JSONOut(map[string]any{"state": "started", "pages": pages, "pagesAdded": pages}, err)
	case "add":
		page, err := page(request.Get("page"))
		if err != nil {
			sessions.Abort()
			return jsu.JSONOut(nil, err)
		}
		count, err := sessions.Add(page)
		return jsu.JSONOut(map[string]any{"pages": count}, err)
	case "finish":
		var info redactOutputInfo
		var err error
		if value := request.Get("revision"); value.Type() == js.TypeUndefined || value.Type() == js.TypeNull {
			info, err = sessions.Finish()
		} else {
			var revision uint64
			revision, err = requiredUint(request, "revision")
			if err == nil {
				info, err = sessions.Finish(revision)
			}
		}
		return jsu.JSONOut(map[string]any{
			"state": "output-ready", "outputRevision": info.OutputRevision, "size": info.Size,
		}, err)
	case "outputRead":
		outputRevision, err := requiredUint(request, "outputRevision")
		var maxBytes uint64
		if err == nil {
			maxBytes, err = requiredUint(request, "maxBytes")
		}
		if err != nil || maxBytes != pdf.PDFBridgeChunkBytes {
			if err == nil {
				err = fmt.Errorf("maxBytes must equal %d", pdf.PDFBridgeChunkBytes)
			}
			return jsu.JSONOut(nil, err)
		}
		chunk, done, err := sessions.OutputRead(outputRevision, int(maxBytes))
		if err != nil {
			return jsu.JSONOut(nil, err)
		}
		meta := map[string]any{"outputRevision": outputRevision, "done": done, "bytes": len(chunk)}
		if done {
			return jsu.JSONOut(meta, nil)
		}
		data := js.Global().Get("Uint8Array").New(len(chunk))
		copied := js.CopyBytesToJS(data, chunk)
		if err := sessions.CompleteOutputRead(outputRevision, chunk, copied); err != nil {
			return jsu.JSONOut(nil, err)
		}
		return dataJSONOut(data, meta, nil)
	case "outputRelease":
		outputRevision, err := requiredUint(request, "outputRevision")
		if err == nil {
			err = sessions.OutputRelease(outputRevision)
		}
		return jsu.JSONOut(map[string]any{"state": "released", "outputRevision": outputRevision}, err)
	case "bridgeStats":
		return jsu.JSONOut(sessions.BridgeStats(), nil)
	case "abort":
		sessions.Abort()
		return jsu.JSONOut(map[string]any{"state": "aborted"}, nil)
	default:
		return jsu.JSONOut(nil, fmt.Errorf("unknown redaction command %q", commandValue.String()))
	}
}

// pdfRun supports the bounded stateful command protocol. The legacy
// pdfRun([{pngData,widthPt,heightPt}], opts) shape remains a one-shot wrapper.
func run(args []js.Value) any {
	if len(args) == 0 {
		return jsu.JSONOut(nil, fmt.Errorf("redaction input is required"))
	}
	first := args[0]
	if first.Type() == js.TypeObject && first.Get("command").Type() == js.TypeString {
		return command(first)
	}
	array := js.Global().Get("Array")
	if array.Type() != js.TypeFunction || !array.Call("isArray", first).Bool() {
		return jsu.JSONOut(nil, fmt.Errorf("redaction input must be a command or page array"))
	}
	pages := make([]pdf.RasterPage, first.Length())
	for i := range pages {
		value, err := page(first.Index(i))
		if err != nil {
			sessions.Abort()
			return jsu.JSONOut(nil, fmt.Errorf("page %d: %w", i+1, err))
		}
		pages[i] = value
	}
	optValue := js.Undefined()
	if len(args) > 1 {
		optValue = args[1]
	}
	opts, err := options(optValue)
	if err != nil {
		sessions.Abort()
		return jsu.JSONOut(nil, err)
	}
	return jsu.Out(sessions.Build(pages, opts))
}

func main() { jsu.Register(run) }
