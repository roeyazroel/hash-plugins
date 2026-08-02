package prediction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	mu                sync.RWMutex
	cfg               Config
	values            map[string]transition
	store             *store
	now               func() time.Time
	ready             chan struct{}
	readyErr          error
	storageErr        error
	readyOnce         sync.Once
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	retryWG           sync.WaitGroup
	retryWake         chan struct{}
	pending           []pendingWrite
	nextPendingID     uint64
	previous          *Outcome
	dbPath            string
	bootstrapPath     string
	preserveBootstrap bool
	closed            bool
}

type pendingWrite struct {
	id   uint64
	key  string
	next transition
	hash bool
}

const maxPendingWrites = 1024

func key(previous, next, cwd string) string { return previous + "\x00" + next + "\x00" + cwd }
func open(ctx context.Context, cfg Config, path string, now func() time.Time) (*Engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	_, existedErr := os.Stat(path)
	existed := existedErr == nil
	if existedErr != nil && !os.IsNotExist(existedErr) {
		return nil, existedErr
	}
	storePath := path
	bootstrapPath := ""
	if cfg.LearnFromOtherShells && !existed {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		bootstrapFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".importing-")
		if err != nil {
			return nil, err
		}
		bootstrapPath = bootstrapFile.Name()
		if err := bootstrapFile.Close(); err != nil {
			_ = os.Remove(bootstrapPath)
			return nil, err
		}
		storePath = bootstrapPath
	}
	st, values, err := openStoreContext(ctx, storePath)
	if err != nil {
		return nil, err
	}
	engineCtx, cancel := context.WithCancel(ctx)
	e := &Engine{
		cfg: cfg, values: values, store: st, now: now, ready: make(chan struct{}),
		cancel: cancel, retryWake: make(chan struct{}, 1), dbPath: path, bootstrapPath: bootstrapPath,
	}
	e.retryWG.Add(1)
	go e.retryPending(engineCtx)
	if cfg.LearnFromOtherShells {
		// A new database is the one-time bootstrap gate; an existing one is never rescanned.
		if !existed {
			e.wg.Add(1)
			go e.bootstrap(engineCtx, path)
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
		e.mu.Lock()
		e.readyErr = ctx.Err()
		e.mu.Unlock()
		return
	default:
	}
	e.mu.Lock()
	if ctx.Err() != nil {
		e.readyErr = ctx.Err()
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
	if err := e.store.putAll(values); err != nil {
		e.readyErr = fmt.Errorf("write imported prediction data: %w", err)
		e.preserveBootstrap = true
		e.mu.Unlock()
		return
	}
	if e.bootstrapPath != "" {
		reopened, loaded, err := promoteBootstrapContext(ctx, e.bootstrapPath, e.dbPath, values, os.Link)
		if err != nil {
			e.readyErr = err
			e.preserveBootstrap = true
			e.mu.Unlock()
			return
		}
		e.store = reopened
		e.values = loaded
		_ = os.Remove(e.bootstrapPath)
		e.bootstrapPath = ""
	}
	e.mu.Unlock()
}

func promoteBootstrap(temporary, final string, values map[string]transition, link func(string, string) error) (*store, map[string]transition, error) {
	return promoteBootstrapContext(context.Background(), temporary, final, values, link)
}

func promoteBootstrapContext(ctx context.Context, temporary, final string, values map[string]transition, link func(string, string) error) (*store, map[string]transition, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	linkErr := link(temporary, final)
	promoted := linkErr == nil
	if linkErr != nil && !errors.Is(linkErr, fs.ErrExist) {
		return nil, nil, fmt.Errorf("promote prediction database: %w", linkErr)
	}
	reopened, loaded, err := openBootstrapStoreContext(ctx, final)
	if err != nil {
		return nil, nil, fmt.Errorf("open promoted prediction database: %w", err)
	}
	if promoted {
		return reopened, loaded, nil
	}
	if err := reopened.mergeAll(values); err != nil {
		return nil, nil, fmt.Errorf("merge imported prediction data: %w", err)
	}
	loaded, err = reopened.load()
	if err != nil {
		return nil, nil, fmt.Errorf("load merged prediction data: %w", err)
	}
	return reopened, loaded, nil
}

func (e *Engine) WaitReady(ctx context.Context) error {
	select {
	case <-e.ready:
		e.mu.RLock()
		defer e.mu.RUnlock()
		return e.readyErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) Observe(o Outcome) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	if e.readyErr != nil {
		return e.readyErr
	}
	if e.storageErr != nil {
		return e.storageErr
	}
	if !success(o) {
		e.previous = nil
		return nil
	}
	var recordErr error
	// The engine derives adjacency from the immediately preceding successful observation.
	if ePrev := e.previous; ePrev != nil {
		recordErr = e.recordLocked(ePrev.Line, o.Line, ePrev.CWD, true)
	}
	e.previous = &Outcome{Line: o.Line, CWD: o.CWD, ExitCode: o.ExitCode}
	return recordErr
}

func (e *Engine) recordLocked(previous, next, cwd string, hash bool) error {
	if !validLine(previous) || !validLine(next) {
		return nil
	}
	k := key(previous, next, cwd)
	t, _, err := e.store.increment(k, transition{Previous: previous, Next: next, CWD: cwd, LastUsed: e.now()}, hash)
	if err != nil {
		if isStoreContention(err) {
			return e.queuePendingLocked(k, transition{Previous: previous, Next: next, CWD: cwd, LastUsed: e.now()}, hash)
		}
		return err
	}
	e.values[k] = t
	return nil
}

func (e *Engine) queuePendingLocked(k string, next transition, hash bool) error {
	if len(e.pending) >= maxPendingWrites {
		e.storageErr = fmt.Errorf("prediction write queue is full")
		return e.storageErr
	}
	e.nextPendingID++
	e.pending = append(e.pending, pendingWrite{id: e.nextPendingID, key: k, next: next, hash: hash})
	select {
	case e.retryWake <- struct{}{}:
	default:
	}
	return nil
}

func (e *Engine) retryPending(ctx context.Context) {
	defer e.retryWG.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.retryWake:
		}
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			remaining, progressed := e.retryOnePending()
			if !remaining {
				break
			}
			if progressed {
				continue
			}
			timer := time.NewTimer(25 * time.Millisecond)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
		}
	}
}

func (e *Engine) retryOnePending() (remaining, progressed bool) {
	e.mu.Lock()
	if e.closed || e.storageErr != nil || len(e.pending) == 0 {
		remaining = len(e.pending) > 0
		e.mu.Unlock()
		return remaining, false
	}
	pending := e.pending[0]
	currentStore := e.store
	e.mu.Unlock()

	t, revision, err := currentStore.increment(pending.key, pending.next, pending.hash)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || len(e.pending) == 0 || e.pending[0].id != pending.id {
		return len(e.pending) > 0, false
	}
	if e.store != currentStore {
		return true, true
	}
	if err != nil {
		if !isStoreContention(err) {
			e.storageErr = fmt.Errorf("persist queued prediction: %w", err)
		}
		return true, false
	}
	if currentStore.hasGeneration(revision) {
		e.values[pending.key] = t
	}
	e.pending = e.pending[1:]
	return len(e.pending) > 0, true
}

func (e *Engine) Suggest(input, cwd string, previous *Previous) string {
	if previous == nil || previous.ExitCode != 0 || previous.Canceled || !validLine(previous.Line) {
		return ""
	}
	e.mu.RLock()
	currentStore := e.store
	unavailable := e.closed || e.readyErr != nil || e.storageErr != nil
	e.mu.RUnlock()
	if unavailable || currentStore == nil {
		return ""
	}
	snapshot, refreshErr := currentStore.refresh()
	if refreshErr == nil && snapshot != nil {
		e.applyRefresh(currentStore, snapshot)
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

func (e *Engine) applyRefresh(currentStore *store, snapshot *refreshSnapshot) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.store != currentStore || !currentStore.accept(snapshot) {
		return false
	}
	e.values = snapshot.values
	return true
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
	// The retry worker exclusively owns queued writes while it is running.
	// Its DB lock wait is bounded, so joining it prevents Close from replaying
	// an increment that committed but has not yet been removed from the queue.
	e.retryWG.Wait()
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.previous = nil
	var closeErr error
	for len(e.pending) > 0 {
		pending := e.pending[0]
		t, _, err := e.store.increment(pending.key, pending.next, pending.hash)
		if err != nil {
			closeErr = fmt.Errorf("flush queued prediction: %w", err)
			break
		}
		e.values[pending.key] = t
		e.pending = e.pending[1:]
	}
	if closeErr == nil {
		if e.storageErr != nil {
			closeErr = e.storageErr
		} else if e.readyErr != nil {
			closeErr = e.readyErr
		}
	}
	if e.store != nil {
		err := e.store.close()
		if e.bootstrapPath != "" && !e.preserveBootstrap {
			_ = os.Remove(e.bootstrapPath)
		}
		e.mu.Unlock()
		if err != nil {
			return errors.Join(closeErr, err)
		}
		return closeErr
	}
	e.mu.Unlock()
	if e.bootstrapPath != "" && !e.preserveBootstrap {
		_ = os.Remove(e.bootstrapPath)
	}
	return closeErr
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
