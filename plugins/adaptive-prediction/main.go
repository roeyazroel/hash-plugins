// Hash's local, successful-sequence adaptive prediction plugin.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/roeyazroel/hash-plugins/internal/prediction"
	"github.com/roeyazroel/hash-plugins/sdk"
)

type finishedParams struct {
	ExecutedLine string `json:"executed_line"`
	OriginalLine string `json:"original_line"`
	ExitCode     int    `json:"exit_code"`
	FailureKind  string `json:"failure_kind"`
	CWD          string `json:"cwd"`
	Canceled     bool   `json:"canceled"`
}
type suggestParams struct {
	Generation uint64 `json:"generation"`
	Trigger    string `json:"trigger"`
	Line       string `json:"line"`
	Cursor     int    `json:"cursor"`
	CWD        string `json:"cwd"`
	Dialect    string `json:"dialect"`
	Previous   *struct {
		Line     string `json:"line"`
		CWD      string `json:"cwd"`
		ExitCode int    `json:"exit_code"`
		Canceled bool   `json:"canceled"`
	} `json:"previous,omitempty"`
}

type pluginState struct {
	mu     sync.Mutex
	engine *prediction.Engine
}

func (p *pluginState) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.engine != nil {
		_ = p.engine.Close()
		p.engine = nil
	}
}
func (p *pluginState) get() *prediction.Engine { p.mu.Lock(); defer p.mu.Unlock(); return p.engine }

func main() {
	server := sdk.New(os.Stdin, os.Stdout)
	state := &pluginState{}
	server.Handle("initialize", func(request sdk.Request) (any, *sdk.Error) {
		var params struct {
			ProtocolVersion int             `json:"protocol_version"`
			Settings        json.RawMessage `json:"settings"`
			SessionKind     string          `json:"session_kind"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ProtocolVersion != 1 {
			return nil, &sdk.Error{Code: -32602, Message: "unsupported protocol version"}
		}
		cfg, err := prediction.ParseConfig(params.Settings)
		if err != nil {
			return nil, &sdk.Error{Code: -32602, Message: "invalid settings"}
		}
		state.close()
		if params.SessionKind != "doctor" {
			engine, err := prediction.Open(context.Background(), cfg, prediction.DefaultDataPath())
			if err != nil {
				return nil, &sdk.Error{Code: -32001, Message: "prediction storage unavailable"}
			}
			state.mu.Lock()
			state.engine = engine
			state.mu.Unlock()
		}
		return map[string]any{"protocol_version": 1}, nil
	})
	server.Handle("command.finished", func(request sdk.Request) (any, *sdk.Error) {
		var params finishedParams
		if json.Unmarshal(request.Params, &params) != nil {
			return nil, &sdk.Error{Code: -32602, Message: "invalid command.finished params"}
		}
		engine := state.get()
		if engine != nil {
			line := params.ExecutedLine
			if line == "" {
				line = params.OriginalLine
			}
			engine.Observe(prediction.Outcome{Line: line, CWD: params.CWD, ExitCode: params.ExitCode, Canceled: params.Canceled, FailureKind: params.FailureKind})
		}
		return map[string]any{"corrections": []string{}}, nil
	})
	server.Handle("editor.suggest", func(request sdk.Request) (any, *sdk.Error) {
		var params suggestParams
		if json.Unmarshal(request.Params, &params) != nil {
			return nil, &sdk.Error{Code: -32602, Message: "invalid editor.suggest params"}
		}
		engine := state.get()
		if engine == nil || params.Previous == nil {
			return map[string]any{"text": ""}, nil
		}
		if err := request.Context.Err(); err != nil {
			return nil, &sdk.Error{Code: -32800, Message: "request canceled"}
		}
		candidate := engine.Suggest(params.Line, params.CWD, &prediction.Previous{Line: params.Previous.Line, CWD: params.Previous.CWD, ExitCode: params.Previous.ExitCode, Canceled: params.Previous.Canceled})
		return map[string]any{"text": candidate}, nil
	})
	server.Handle("shutdown", func(sdk.Request) (any, *sdk.Error) { state.close(); return map[string]any{}, nil })
	if err := server.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, "hash-adaptive-prediction:", err)
		os.Exit(1)
	}
}
