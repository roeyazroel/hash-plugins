package autosuggestion

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeHistory struct {
	mu      sync.Mutex
	results map[string][]HistoryEntry
	errors  map[string]error
	calls   map[string]int
	started chan string
	release chan struct{}
}

func (f *fakeHistory) Query(ctx context.Context, _ int64, prefix, cwd string, _ int) ([]HistoryEntry, error) {
	if f.started != nil {
		f.started <- cwd
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != nil {
		f.calls[cwd]++
	}
	return append([]HistoryEntry(nil), f.results[cwd]...), f.errors[cwd]
}

func TestEngineRanksCurrentCWDThenGlobalThenImported(t *testing.T) {
	imported := &memoryImported{shells: []string{"zsh", "bash"}, entries: map[string][]string{
		"zsh":  {"git imported-new", "git imported-old"},
		"bash": {"git bash-newer"},
	}}
	cases := []struct {
		name    string
		results map[string][]HistoryEntry
		errors  map[string]error
		want    string
	}{
		{"cwd wins", map[string][]HistoryEntry{"/work": {{Line: "git local"}}, "": {{Line: "git global"}}}, nil, "git local"},
		{"global fallback", map[string][]HistoryEntry{"": {{Line: "git global"}}}, nil, "git global"},
		{"healthy global degrades", map[string][]HistoryEntry{"": {{Line: "git global"}}}, map[string]error{"/work": errors.New("down")}, "git global"},
		{"imported fallback", map[string][]HistoryEntry{}, map[string]error{"/work": errors.New("down"), "": errors.New("down")}, "git imported-new"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := NewEngine(DefaultConfig(), &fakeHistory{results: tc.results, errors: tc.errors}, imported)
			got, err := engine.Suggest(context.Background(), 7, SuggestRequest{Trigger: "edit", Line: "git", Cursor: 3, CWD: "/work", Dialect: "bash"})
			if err != nil || got != tc.want {
				t.Fatalf("Suggest() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestEngineQueriesCurrentAndGlobalHistoryConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	history := &fakeHistory{results: map[string][]HistoryEntry{}, errors: map[string]error{}, started: started, release: release}
	engine := NewEngine(DefaultConfig(), history, nil)
	done := make(chan error, 1)
	go func() {
		_, err := engine.Suggest(context.Background(), 9, SuggestRequest{Trigger: "edit", Line: "gi", Cursor: 2, CWD: "/work", Dialect: "bash"})
		done <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("history queries did not start concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEngineInputAndSafetyFilters(t *testing.T) {
	history := &fakeHistory{results: map[string][]HistoryEntry{
		"/work": {
			{Line: "gi"},
			{Line: "git status\nrm -rf /"},
			{Line: "git --password=secret"},
			{Line: "git push --token secret"},
			{Line: "git push AWS_ACCESS_KEY_ID=AKIAEXAMPLE"},
			{Line: "git push AWS_SECRET_ACCESS_KEY=example"},
			{Line: "git clone https://user:pass@example.com/repo"},
			{Line: "git curl -u user:pass"},
			{Line: "git |"},
			{Line: "git 'unterminated"},
			{Line: "git status"},
		},
	}, errors: map[string]error{}}
	engine := NewEngine(DefaultConfig(), history, nil)
	for _, req := range []SuggestRequest{
		{Trigger: "prompt", Line: "git", Cursor: 3, CWD: "/work", Dialect: "bash"},
		{Trigger: "edit", Line: "g", Cursor: 1, CWD: "/work", Dialect: "bash"},
		{Trigger: "edit", Line: "git", Cursor: 2, CWD: "/work", Dialect: "bash"},
	} {
		got, err := engine.Suggest(context.Background(), 1, req)
		if err != nil || got != "" {
			t.Fatalf("ineligible request %+v = %q, %v", req, got, err)
		}
	}
	got, err := engine.Suggest(context.Background(), 1, SuggestRequest{Trigger: "edit", Line: "git", Cursor: 3, CWD: "/work", Dialect: "bash"})
	if err != nil || got != "git status" {
		t.Fatalf("filtered suggestion = %q, %v", got, err)
	}
}

func TestEngineDoesNotDuplicateEmptyCWDQuery(t *testing.T) {
	history := &fakeHistory{results: map[string][]HistoryEntry{}, errors: map[string]error{}, calls: map[string]int{}}
	engine := NewEngine(DefaultConfig(), history, nil)
	if _, err := engine.Suggest(context.Background(), 1, SuggestRequest{Trigger: "edit", Line: "gi", Cursor: 2, Dialect: "bash"}); err != nil {
		t.Fatal(err)
	}
	if got := history.calls[""]; got != 1 {
		t.Fatalf("global history queries = %d, want 1", got)
	}
}

func TestEngineAcceptsValidANSICQuoting(t *testing.T) {
	line := `echo $'can\'t'`
	history := &fakeHistory{results: map[string][]HistoryEntry{"/work": {{Line: line}}}, errors: map[string]error{}}
	engine := NewEngine(DefaultConfig(), history, nil)
	got, err := engine.Suggest(context.Background(), 1, SuggestRequest{Trigger: "edit", Line: "ec", Cursor: 2, CWD: "/work", Dialect: "bash"})
	if err != nil || got != line {
		t.Fatalf("ANSI-C suggestion = %q, %v", got, err)
	}
}

func BenchmarkImportedNoMatch150000(b *testing.B) {
	commands := make([]string, 50000)
	for i := range commands {
		commands[i] = "unrelated-command-" + stringOf('x', i%32)
	}
	imported := &memoryImported{
		shells: []string{"zsh", "bash", "fish"},
		entries: map[string][]string{
			"zsh":  commands,
			"bash": commands,
			"fish": commands,
		},
	}
	b.ResetTimer()
	for range b.N {
		if got := imported.Suggest("git", "bash"); got != "" {
			b.Fatal(got)
		}
	}
}

func TestEngineCancellationReturnsCancellation(t *testing.T) {
	release := make(chan struct{})
	history := &fakeHistory{results: map[string][]HistoryEntry{}, errors: map[string]error{}, release: release}
	engine := NewEngine(DefaultConfig(), history, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Suggest(ctx, 1, SuggestRequest{Trigger: "edit", Line: "gi", Cursor: 2, CWD: "/work", Dialect: "bash"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestEngineCancellationInterruptsLargeImportedFallback(t *testing.T) {
	commands := make([]string, 50000)
	for i := range commands {
		commands[i] = "unrelated-command-" + stringOf('x', i%32)
	}
	started := make(chan struct{})
	var startedOnce sync.Once
	cache := &Cache{
		shells: []string{"zsh", "bash", "fish"},
		entries: map[string][]string{
			"zsh":  commands,
			"bash": commands,
			"fish": commands,
		},
		scanHook: func(ctx context.Context) {
			startedOnce.Do(func() { close(started) })
			<-ctx.Done()
		},
	}
	history := &fakeHistory{
		results: map[string][]HistoryEntry{},
		errors:  map[string]error{"/work": errors.New("down"), "": errors.New("down")},
	}
	engine := NewEngine(DefaultConfig(), history, cache)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := engine.Suggest(ctx, 1, SuggestRequest{Trigger: "edit", Line: "git", Cursor: 3, CWD: "/work", Dialect: "bash"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("imported fallback scan did not start")
	}
	startedAt := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("canceled imported fallback did not return promptly")
	}
	if elapsed := time.Since(startedAt); elapsed >= 100*time.Millisecond {
		t.Fatalf("canceled imported fallback took %s", elapsed)
	}
}

func TestEngineDeadlineInterruptsLargeImportedFallback(t *testing.T) {
	commands := make([]string, 50000)
	for i := range commands {
		commands[i] = "unrelated-command-" + stringOf('x', i%32)
	}
	started := make(chan struct{})
	var startedOnce sync.Once
	cache := &Cache{
		shells: []string{"zsh", "bash", "fish"},
		entries: map[string][]string{
			"zsh":  commands,
			"bash": commands,
			"fish": commands,
		},
		scanHook: func(ctx context.Context) {
			startedOnce.Do(func() { close(started) })
			<-ctx.Done()
		},
	}
	history := &fakeHistory{
		results: map[string][]HistoryEntry{},
		errors:  map[string]error{"/work": errors.New("down"), "": errors.New("down")},
	}
	engine := NewEngine(DefaultConfig(), history, cache)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := engine.Suggest(ctx, 1, SuggestRequest{Trigger: "edit", Line: "git", Cursor: 3, CWD: "/work", Dialect: "bash"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("imported fallback scan did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want deadline exceeded", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expired imported fallback did not return promptly")
	}
}

func TestEngineAllowsLongSingleLineSuggestions(t *testing.T) {
	long := "echo wrapped-" + stringOf('x', 800)
	history := &fakeHistory{results: map[string][]HistoryEntry{"/work": {{Line: long}}}, errors: map[string]error{}}
	engine := NewEngine(DefaultConfig(), history, nil)
	got, err := engine.Suggest(context.Background(), 1, SuggestRequest{Trigger: "edit", Line: "ec", Cursor: 2, CWD: "/work", Dialect: "bash"})
	if err != nil || got != long {
		t.Fatalf("long suggestion length = %d, err=%v", len(got), err)
	}
}

func stringOf(r byte, count int) string {
	value := make([]byte, count)
	for i := range value {
		value[i] = r
	}
	return string(value)
}
