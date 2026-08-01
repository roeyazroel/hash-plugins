// Hash's local, literal-prefix history autosuggestion plugin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/roeyazroel/hash-plugins/internal/autosuggestion"
	"github.com/roeyazroel/hash-plugins/sdk"
)

type historyClient struct {
	server *sdk.Server
}

func (h historyClient) Query(ctx context.Context, parentID int64, prefix, cwd string, limit int) ([]autosuggestion.HistoryEntry, error) {
	var result struct {
		Entries []autosuggestion.HistoryEntry `json:"entries"`
	}
	err := h.server.Call(ctx, parentID, "host.history.query", map[string]any{
		"prefix": prefix,
		"cwd":    cwd,
		"limit":  limit,
	}, &result)
	return result.Entries, err
}

type pluginState struct {
	mu     sync.RWMutex
	engine *autosuggestion.Engine
	cache  *autosuggestion.Cache
}

func (p *pluginState) replace(engine *autosuggestion.Engine, cache *autosuggestion.Cache) {
	p.mu.Lock()
	previous := p.cache
	p.engine = engine
	p.cache = cache
	p.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
}

func (p *pluginState) get() *autosuggestion.Engine {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.engine
}

func (p *pluginState) close() {
	p.replace(nil, nil)
}

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
		cfg, err := autosuggestion.ParseConfig(params.Settings)
		if err != nil {
			return nil, &sdk.Error{Code: -32602, Message: "invalid settings"}
		}
		state.close()
		if params.SessionKind == "doctor" {
			return map[string]any{"protocol_version": 1}, nil
		}
		dataPath, err := autosuggestion.DefaultDataPath()
		if err != nil {
			return nil, &sdk.Error{Code: -32001, Message: "autosuggestion storage unavailable"}
		}
		cache, err := autosuggestion.OpenCache(context.Background(), cfg, dataPath)
		if err != nil {
			return nil, &sdk.Error{Code: -32001, Message: "autosuggestion storage unavailable"}
		}
		state.replace(autosuggestion.NewEngine(cfg, historyClient{server: server}, cache), cache)
		return map[string]any{"protocol_version": 1}, nil
	})
	server.Handle("editor.suggest", func(request sdk.Request) (any, *sdk.Error) {
		var params autosuggestion.SuggestRequest
		if json.Unmarshal(request.Params, &params) != nil {
			return nil, &sdk.Error{Code: -32602, Message: "invalid editor.suggest params"}
		}
		engine := state.get()
		if engine == nil {
			return map[string]any{"text": ""}, nil
		}
		candidate, err := engine.Suggest(request.Context, request.ID, params)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, &sdk.Error{Code: -32800, Message: "request canceled"}
		}
		if err != nil {
			return map[string]any{"text": ""}, nil
		}
		return map[string]any{"text": candidate}, nil
	})
	server.Handle("shutdown", func(sdk.Request) (any, *sdk.Error) {
		state.close()
		return map[string]any{}, nil
	})
	if err := server.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, "hash-autosuggestions: protocol unavailable")
		os.Exit(1)
	}
}
