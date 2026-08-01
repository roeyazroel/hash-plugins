package prediction

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	mu            sync.RWMutex
	cfg           Config
	values        map[string]transition
	store         *store
	now           func() time.Time
	ready         chan struct{}
	readyOnce     sync.Once
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	previous      *Outcome
	dbPath        string
	bootstrapPath string
	closed        bool
}

func key(previous, next, cwd string) string { return previous + "\x00" + next + "\x00" + cwd }
func open(ctx context.Context, cfg Config, path string, now func() time.Time) (*Engine, error) {
	if now == nil {
		now = time.Now
	}
	_, existedErr := os.Stat(path)
	existed := existedErr == nil
	storePath := path
	bootstrapPath := ""
	if cfg.LearnFromOtherShells && !existed {
		bootstrapPath = path + ".importing"
		_ = os.Remove(bootstrapPath)
		storePath = bootstrapPath
	}
	st, values, err := openStore(storePath)
	if err != nil {
		return nil, err
	}
	e := &Engine{cfg: cfg, values: values, store: st, now: now, ready: make(chan struct{}), dbPath: path, bootstrapPath: bootstrapPath}
	if cfg.LearnFromOtherShells {
		// A new database is the one-time bootstrap gate; an existing one is never rescanned.
		if !existed {
			bootstrapCtx, cancel := context.WithCancel(ctx)
			e.cancel = cancel
			e.wg.Add(1)
			go e.bootstrap(bootstrapCtx, path)
		} else {
			close(e.ready)
		}
	} else {
		close(e.ready)
	}
	return e, nil
}

// Open starts an interactive prediction engine. Doctor sessions must not call
// this function because opening the store is intentionally a data-creating act.
func Open(ctx context.Context, cfg Config, path string) (*Engine, error) {
	return open(ctx, cfg, path, time.Now)
}

func (e *Engine) bootstrap(ctx context.Context, path string) {
	defer e.wg.Done()
	defer e.readyOnce.Do(func() { close(e.ready) })
	paths := defaultHistoryPaths(e.cfg.Shells)
	for shell, configured := range e.cfg.HistoryPaths {
		paths[shell] = configured
	}
	seq, _ := importHistories(paths, e.cfg.Shells)
	select {
	case <-ctx.Done():
		return
	default:
	}
	e.mu.Lock()
	if ctx.Err() != nil {
		e.mu.Unlock()
		return
	}
	for _, pair := range seq {
		if pair[0] == "" || pair[1] == "" {
			continue
		}
		k := key(pair[0], pair[1], "")
		t := e.values[k]
		t.Previous = pair[0]
		t.Next = pair[1]
		t.CWD = ""
		t.ImportedCount++
		t.LastUsed = e.now()
		e.values[k] = t
	}
	values := make(map[string]transition, len(e.values))
	for k, v := range e.values {
		values[k] = v
	}
	if e.store.putAll(values) == nil && e.bootstrapPath != "" {
		_ = e.store.close()
		if os.Rename(e.bootstrapPath, e.dbPath) == nil {
			if reopened, loaded, err := openStore(e.dbPath); err == nil {
				e.store = reopened
				e.values = loaded
				e.bootstrapPath = ""
			}
		}
	}
	e.mu.Unlock()
}

func (e *Engine) WaitReady(ctx context.Context) error {
	select {
	case <-e.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) Observe(o Outcome) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	if !success(o) {
		e.previous = nil
		return
	}
	// The engine derives adjacency from the immediately preceding successful observation.
	if ePrev := e.previous; ePrev != nil {
		_ = e.recordLocked(ePrev.Line, o.Line, ePrev.CWD, true)
	}
	e.previous = &Outcome{Line: o.Line, CWD: o.CWD, ExitCode: o.ExitCode}
}

func (e *Engine) recordLocked(previous, next, cwd string, hash bool) error {
	if !validLine(previous) || !validLine(next) {
		return nil
	}
	k := key(previous, next, cwd)
	t := e.values[k]
	t.Previous = previous
	t.Next = next
	t.CWD = cwd
	if hash {
		t.HashCount++
	}
	t.LastUsed = e.now()
	e.values[k] = t
	return e.store.put(k, t)
}

func (e *Engine) Suggest(input, cwd string, previous *Previous) string {
	if previous == nil || previous.ExitCode != 0 || previous.Canceled || !validLine(previous.Line) {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return ""
	}
	targetCWD := cwd
	if previous.CWD != "" {
		targetCWD = previous.CWD
	}
	best := candidate{}
	for _, t := range e.values {
		if t.Previous != previous.Line || (t.CWD != "" && t.CWD != targetCWD) || t.HashCount < 1 {
			continue
		}
		if input != "" && (!strings.HasPrefix(t.Next, input) || t.Next == input) {
			continue
		}
		total := t.HashCount + t.ImportedCount
		age := e.now().Sub(t.LastUsed).Hours()
		score := .7*float64(total)/float64(total+5) + .3/(1+math.Max(age, 0)/24)
		exact := t.CWD == targetCWD
		if score < e.cfg.ConfidenceThreshold {
			continue
		}
		c := candidate{line: t.Next, score: score, exact: exact, count: total, when: t.LastUsed}
		if c.better(best) {
			best = c
		}
	}
	return best.line
}

type candidate struct {
	line  string
	score float64
	exact bool
	count int
	when  time.Time
}

func (c candidate) better(b candidate) bool {
	if c.line == "" {
		return false
	}
	if b.line == "" {
		return true
	}
	if c.exact != b.exact {
		return c.exact
	}
	if math.Abs(c.score-b.score) > 1e-9 {
		return c.score > b.score
	}
	if c.count != b.count {
		return c.count > b.count
	}
	if !c.when.Equal(b.when) {
		return c.when.After(b.when)
	}
	return c.line < b.line
}

func (e *Engine) Close() error {
	if e.cancel != nil {
		e.cancel()
	}
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.previous = nil
	if e.store != nil {
		err := e.store.close()
		if e.bootstrapPath != "" {
			_ = os.Remove(e.bootstrapPath)
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		return nil
	}
	e.mu.Unlock()
	if e.bootstrapPath != "" {
		_ = os.Remove(e.bootstrapPath)
	}
	return nil
}

func DefaultDataPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "hash", "plugin-data", "io.runhash.adaptive-prediction", "prediction.db")
}

func (e *Engine) MarshalJSON() ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return json.Marshal(e.values)
}
