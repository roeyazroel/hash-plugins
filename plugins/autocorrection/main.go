// Hash's separately-built conservative autocorrection plugin.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tfcace/hash-plugins/internal/correction"
	"github.com/tfcace/hash-plugins/sdk"
)

type hostClient struct {
	server   *sdk.Server
	parentID int64
}

func (h hostClient) History(ctx context.Context, prefix, cwd string, limit int) ([]correction.HistoryEntry, error) {
	var result struct {
		Entries []struct {
			Line      string `json:"line"`
			CWD       string `json:"cwd"`
			ExitCode  int    `json:"exit_code"`
			Timestamp string `json:"timestamp"`
		} `json:"entries"`
	}
	err := h.server.Call(ctx, h.parentID, "host.history.query", map[string]any{"prefix": prefix, "cwd": cwd, "limit": limit}, &result)
	entries := make([]correction.HistoryEntry, 0, len(result.Entries))
	for _, e := range result.Entries {
		entries = append(entries, correction.HistoryEntry{Line: e.Line, CWD: e.CWD, ExitCode: e.ExitCode, Timestamp: e.Timestamp})
	}
	return entries, err
}
func (h hostClient) Completion(ctx context.Context, line string, cursor int) ([]correction.CompletionItem, error) {
	var result struct {
		Items []struct {
			Label      string `json:"label"`
			InsertText string `json:"insert_text"`
		} `json:"items"`
	}
	err := h.server.Call(ctx, h.parentID, "host.completion.query", map[string]any{"line": line, "cursor": cursor}, &result)
	items := make([]correction.CompletionItem, 0, len(result.Items))
	for _, i := range result.Items {
		items = append(items, correction.CompletionItem{Label: i.Label, InsertText: i.InsertText})
	}
	return items, err
}

func main() {
	server := sdk.New(os.Stdin, os.Stdout)
	defaults := correction.DefaultConfig()
	executables := correction.DiscoverExecutables(os.Getenv("PATH"))
	engine := correction.Engine{HistoryLimit: defaults.HistoryLimit, MaxCandidates: defaults.MaxCandidates, Strategies: defaults.Strategies, Executables: executables}
	server.Handle("initialize", func(request sdk.Request) (any, *sdk.Error) {
		var params struct {
			ProtocolVersion int             `json:"protocol_version"`
			Settings        json.RawMessage `json:"settings"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProtocolVersion != 1 {
			return nil, &sdk.Error{Code: -32602, Message: "unsupported protocol version"}
		}
		cfg, err := correction.ParseConfig(params.Settings)
		if err != nil {
			return nil, &sdk.Error{Code: -32602, Message: "invalid settings: " + err.Error()}
		}
		engine = correction.Engine{HistoryLimit: cfg.HistoryLimit, MaxCandidates: cfg.MaxCandidates, Strategies: cfg.Strategies, Executables: executables}
		return map[string]any{"protocol_version": 1}, nil
	})
	server.Handle("command.finished", func(request sdk.Request) (any, *sdk.Error) {
		var params correction.Params
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, &sdk.Error{Code: -32602, Message: "invalid command.finished params"}
		}
		candidates := engine.Correct(request.Context, hostClient{server: server, parentID: request.ID}, params)
		return map[string]any{"corrections": candidates}, nil
	})
	server.Handle("shutdown", func(sdk.Request) (any, *sdk.Error) { return map[string]any{}, nil })
	if err := server.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, "hash-autocorrection:", err)
		os.Exit(1)
	}
}
