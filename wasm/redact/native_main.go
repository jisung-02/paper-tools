//go:build !js || !wasm

package main

// The real entry point lives in main.go behind `js && wasm`. This native stub
// lets `go build ./...`, gopls, and `go test ./wasm/redact/...` link the
// package off-target so session.go's logic stays unit-testable natively.
func main() {}
