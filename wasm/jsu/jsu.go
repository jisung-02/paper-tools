//go:build js && wasm

// Package jsu holds the small set of syscall/js helpers shared by every
// per-tool wasm binary: pulling bytes out of JS values, and packaging Go
// results back into the {data:...} / {json:...} / {error:...} shapes that
// web/app.js expects.
package jsu

import (
	"encoding/json"
	"fmt"
	"syscall/js"
)

// Bytes copies a JS Uint8Array (or any TypedArray view) into a Go []byte.
func Bytes(v js.Value) []byte {
	n := v.Get("byteLength").Int()
	buf := make([]byte, n)
	js.CopyBytesToGo(buf, v)
	return buf
}

// FileList converts a JS array of Uint8Array into [][]byte.
func FileList(v js.Value) [][]byte {
	n := v.Length()
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i] = Bytes(v.Index(i))
	}
	return out
}

// toJS copies a Go []byte into a freshly allocated JS Uint8Array.
func toJS(data []byte) js.Value {
	arr := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(arr, data)
	return arr
}

// Out packages a pdf operation's ([]byte, error) result for JS:
// {"data": Uint8Array} on success, {"error": message} on failure.
func Out(data []byte, err error) any {
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"data": toJS(data)}
}

// JSONOut packages a (value, error) result for JS by JSON-encoding value:
// {"json": string} on success, {"error": message} on failure.
func JSONOut(v any, err error) any {
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"json": string(b)}
}

// Register installs fn as the page's single global entry point, "pdfRun",
// then blocks forever so the wasm module stays alive to serve calls.
//
// ponytail: this file is js/wasm-only (build tag js && wasm), so the panic
// recovery below can't be exercised by plain `go test`; it's verified by
// building for GOOS=js GOARCH=wasm and by manual testing in the browser.
func Register(fn func(args []js.Value) any) {
	js.Global().Set("pdfRun", js.FuncOf(func(this js.Value, args []js.Value) (result any) {
		defer func() {
			if r := recover(); r != nil {
				result = map[string]any{"error": fmt.Sprintf("처리 중 오류가 발생했습니다: %v", r)}
			}
		}()
		return fn(args)
	}))
	select {}
}
