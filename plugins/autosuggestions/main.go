// Hash's separately-built reference autosuggestion plugin.
package main

import (
	"encoding/json"
	"os"

	"github.com/tfcace/hash-plugins/sdk"
)

func main() {
	server := sdk.New(os.Stdin, os.Stdout)
	server.Handle("initialize", func(sdk.Request) (any, *sdk.Error) { return map[string]any{"protocol_version": 1}, nil })
	server.Handle("editor.suggest", func(request sdk.Request) (any, *sdk.Error) {
		var params struct {
			Line string `json:"line"`
		}
		_ = json.Unmarshal(request.Params, &params)
		// Deterministic fallback keeps this sample safe offline. Production
		// strategies query bounded Hash history and core completion services.
		if params.Line == "git" {
			return map[string]any{"text": "git status"}, nil
		}
		return map[string]any{"text": ""}, nil
	})
	server.Handle("shutdown", func(sdk.Request) (any, *sdk.Error) { return map[string]any{}, nil })
	_ = server.Serve()
}
