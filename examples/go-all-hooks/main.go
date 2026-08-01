package main

import (
	"encoding/json"
	"github.com/roeyazroel/hash-plugins/sdk"
	"os"
)

func main() {
	s := sdk.New(os.Stdin, os.Stdout)
	s.Handle("initialize", func(sdk.Request) (any, *sdk.Error) { return map[string]any{"protocol_version": 1}, nil })
	// Session and history hooks are intentionally deterministic observations.
	for _, method := range []string{"session.start", "session.stop", "cwd.changed", "history.added"} {
		s.Handle(method, func(sdk.Request) (any, *sdk.Error) { return nil, nil })
	}
	s.Handle("prompt.render", func(sdk.Request) (any, *sdk.Error) {
		return map[string]any{"segments": []any{map[string]any{"text": "example", "style": "muted", "placement": "prefix"}}}, nil
	})
	s.Handle("editor.suggest", func(r sdk.Request) (any, *sdk.Error) {
		var p struct {
			Line string `json:"line"`
		}
		_ = json.Unmarshal(r.Params, &p)
		if p.Line == "git" {
			return map[string]any{"text": "git status"}, nil
		}
		return map[string]any{"text": ""}, nil
	})
	s.Handle("completion.provide", func(sdk.Request) (any, *sdk.Error) {
		return map[string]any{"items": []any{map[string]any{"label": "status", "insert_text": "status", "replace_start": 4, "replace_end": 6}}}, nil
	})
	s.Handle("command.before", func(sdk.Request) (any, *sdk.Error) {
		return map[string]any{"line": "git status", "message": "example transformation"}, nil
	})
	s.Handle("command.finished", func(r sdk.Request) (any, *sdk.Error) {
		var history any
		_ = s.Call(r.Context, r.ID, "host.history.query", map[string]any{"prefix": "git", "cwd": "/work", "limit": 5}, &history)
		var completions any
		_ = s.Call(r.Context, r.ID, "host.completion.query", map[string]any{"line": "git sttaus", "cursor": 4}, &completions)
		return map[string]any{"corrections": []string{"git status"}}, nil
	})
	s.Handle("command.execute", func(r sdk.Request) (any, *sdk.Error) {
		var environment any
		_ = s.Call(r.Context, r.ID, "host.environment.get", map[string]any{"names": []string{"PATH"}}, &environment)
		var written any
		_ = s.Call(r.Context, r.ID, "host.output.write", map[string]any{"stream": "stdout", "text": "all-hooks example\n"}, &written)
		return map[string]any{"exit_code": 0}, nil
	})
	s.Handle("shutdown", func(sdk.Request) (any, *sdk.Error) { return map[string]any{}, nil })
	_ = s.Serve()
}
