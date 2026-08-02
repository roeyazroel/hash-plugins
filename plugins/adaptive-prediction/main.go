// Hash's local, successful-sequence adaptive prediction plugin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	mu         sync.Mutex
	lifecycle  sync.Mutex
	engine     *prediction.Engine
	opener     engineOpener
	openCancel context.CancelFunc
	generation uint64
	openWG     sync.WaitGroup
	retiredWG  sync.WaitGroup
	retiredErr error
}

type engineOpener func(context.Context, prediction.Config, string) (*prediction.Engine, error)

func newPluginState(opener engineOpener) *pluginState {
	if opener == nil {
		opener = prediction.Open
	}
	return &pluginState{opener: opener}
}

func (p *pluginState) close() error {
	p.lifecycle.Lock()
	p.mu.Lock()
	p.generation++
	if p.openCancel != nil {
		p.openCancel()
		p.openCancel = nil
	}
	engine := p.engine
	p.engine = nil
	p.mu.Unlock()
	// A storage opener that finishes after cancellation closes its own engine
	// before it releases this wait group. Retired engines are tracked
	// separately, so shutdown cannot return while either one is still live.
	p.openWG.Wait()
	p.retiredWG.Wait()
	p.mu.Lock()
	retiredErr := p.retiredErr
	p.retiredErr = nil
	p.mu.Unlock()
	if engine != nil {
		err := engine.Close()
		p.lifecycle.Unlock()
		return errors.Join(retiredErr, err)
	}
	p.lifecycle.Unlock()
	return retiredErr
}

func (p *pluginState) recordRetiredError(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.retiredErr = errors.Join(p.retiredErr, err)
	p.mu.Unlock()
}

func (p *pluginState) start(cfg prediction.Config, path string) {
	p.lifecycle.Lock()
	p.mu.Lock()
	p.generation++
	generation := p.generation
	if p.openCancel != nil {
		p.openCancel()
	}
	old := p.engine
	p.engine = nil
	ctx, cancel := context.WithCancel(context.Background())
	p.openCancel = cancel
	p.openWG.Add(1)
	if old != nil {
		p.retiredWG.Add(1)
	}
	p.mu.Unlock()
	p.lifecycle.Unlock()

	if old != nil {
		go func() {
			defer p.retiredWG.Done()
			p.recordRetiredError(old.Close())
		}()
	}
	go func() {
		defer p.openWG.Done()
		engine, err := p.opener(ctx, cfg, path)
		if err != nil {
			if engine != nil {
				p.recordRetiredError(engine.Close())
			}
			return
		}
		if engine == nil {
			return
		}
		p.mu.Lock()
		if p.generation == generation {
			p.engine = engine
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		p.recordRetiredError(engine.Close())
	}()
}

func (p *pluginState) get() *prediction.Engine { p.mu.Lock(); defer p.mu.Unlock(); return p.engine }

func main() {
	server := newServer(os.Stdin, os.Stdout, newPluginState(prediction.Open))
	if err := server.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, "hash-adaptive-prediction:", err)
		os.Exit(1)
	}
}

func newServer(in io.Reader, out io.Writer, state *pluginState) *sdk.Server {
	server := sdk.New(in, out)
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
		if params.SessionKind != "doctor" {
			state.start(cfg, prediction.DefaultDataPath())
		} else if err := state.close(); err != nil {
			return nil, &sdk.Error{Code: -32001, Message: "prediction storage unavailable"}
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
			if err := engine.Observe(prediction.Outcome{Line: line, CWD: params.CWD, ExitCode: params.ExitCode, Canceled: params.Canceled, FailureKind: params.FailureKind}); err != nil {
				return nil, &sdk.Error{Code: -32001, Message: "prediction storage unavailable"}
			}
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
	server.Handle("shutdown", func(sdk.Request) (any, *sdk.Error) {
		if err := state.close(); err != nil {
			return nil, &sdk.Error{Code: -32001, Message: "prediction storage unavailable"}
		}
		return map[string]any{}, nil
	})
	return server
}
