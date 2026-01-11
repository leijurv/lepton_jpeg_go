//go:build js && wasm

package main

import (
	"bytes"
	"syscall/js"

	"github.com/leijurv/lepton_jpeg_go/lepton"
)

func decodeLepton(this js.Value, args []js.Value) interface{} {
	if len(args) != 1 {
		return js.ValueOf(map[string]interface{}{
			"error": "expected 1 argument (Uint8Array)",
		})
	}

	// Get input Uint8Array
	inputArray := args[0]
	inputLen := inputArray.Get("length").Int()
	input := make([]byte, inputLen)
	js.CopyBytesToGo(input, inputArray)

	// Decode lepton to JPEG
	var output bytes.Buffer
	err := lepton.DecodeLepton(bytes.NewReader(input), &output)
	if err != nil {
		return js.ValueOf(map[string]interface{}{
			"error": err.Error(),
		})
	}

	// Create output Uint8Array
	result := output.Bytes()
	outputArray := js.Global().Get("Uint8Array").New(len(result))
	js.CopyBytesToJS(outputArray, result)

	return js.ValueOf(map[string]interface{}{
		"data": outputArray,
	})
}

func main() {
	js.Global().Set("leptonDecode", js.FuncOf(decodeLepton))

	// Keep the Go program running
	select {}
}
