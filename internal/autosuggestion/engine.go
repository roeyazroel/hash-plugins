package autosuggestion

import (
	"context"
	"unicode/utf8"
)

type SuggestRequest struct {
	Generation uint64
	Trigger    string
	Line       string
	Cursor     int
	CWD        string
	Dialect    string
}

type HistoryEntry struct {
	Line      string `json:"line"`
	CWD       string `json:"cwd"`
	ExitCode  int    `json:"exit_code"`
	Timestamp string `json:"timestamp"`
}

type HistoryQuerier interface {
	Query(context.Context, int64, string, string, int) ([]HistoryEntry, error)
}

type importedSource interface {
	SuggestContext(context.Context, string, string) (string, error)
}

type Engine struct {
	cfg      Config
	history  HistoryQuerier
	imported importedSource
}

func NewEngine(cfg Config, history HistoryQuerier, imported importedSource) *Engine {
	if cfg.HistoryLimit < 1 || cfg.HistoryLimit > 100 {
		cfg.HistoryLimit = 100
	}
	return &Engine{cfg: cfg, history: history, imported: imported}
}

type queryResult struct {
	cwd     string
	entries []HistoryEntry
	err     error
}

func (e *Engine) Suggest(ctx context.Context, parentID int64, request SuggestRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if request.Trigger != "edit" || request.Cursor != len(request.Line) || utf8.RuneCountInString(request.Line) < 2 || !utf8.ValidString(request.Line) {
		return "", nil
	}
	scopes := []string{""}
	if request.CWD != "" {
		scopes = []string{request.CWD, ""}
	}
	results := make(chan queryResult, len(scopes))
	for _, cwd := range scopes {
		cwd := cwd
		go func() {
			if e.history == nil {
				results <- queryResult{cwd: cwd}
				return
			}
			entries, err := e.history.Query(ctx, parentID, request.Line, cwd, e.cfg.HistoryLimit)
			results <- queryResult{cwd: cwd, entries: entries, err: err}
		}()
	}
	byCWD := map[string]queryResult{}
	for range scopes {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case result := <-results:
			byCWD[result.cwd] = result
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, cwd := range scopes {
		result := byCWD[cwd]
		if result.err != nil {
			continue
		}
		for _, entry := range result.entries {
			if entry.ExitCode == 0 && validCandidate(request.Line, entry.Line, request.Dialect) {
				return entry.Line, nil
			}
		}
	}
	if e.imported != nil {
		return e.imported.SuggestContext(ctx, request.Line, request.Dialect)
	}
	return "", nil
}

type memoryImported struct {
	shells  []string
	entries map[string][]string
}

func (m *memoryImported) Suggest(prefix, dialect string) string {
	suggestion, _ := m.SuggestContext(context.Background(), prefix, dialect)
	return suggestion
}

func (m *memoryImported) SuggestContext(ctx context.Context, prefix, dialect string) (string, error) {
	if m == nil {
		return "", nil
	}
	return scanImported(ctx, m.shells, m.entries, nil, prefix, dialect)
}
