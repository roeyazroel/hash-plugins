package prediction

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

func TestEngineLearnsOnlyAdjacentSuccessfulTransitions(t *testing.T) {
	engine := newTestEngine(t, Config{ConfidenceThreshold: 0.01})

	observe(t, engine, Outcome{Line: "git status", CWD: "/work", ExitCode: 1, FailureKind: "exit_status"})
	observe(t, engine, Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
	if got := engine.Suggest("", "/work", &Previous{Line: "git pull", CWD: "/work", ExitCode: 0}); got != "" {
		t.Fatalf("suggestion after failed predecessor = %q", got)
	}

	observe(t, engine, Outcome{Line: "git status", CWD: "/work", ExitCode: 0})
	observe(t, engine, Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
	if got := engine.Suggest("", "/work", &Previous{Line: "git status", CWD: "/work", ExitCode: 0}); got != "git pull" {
		t.Fatalf("suggestion after successful transition = %q", got)
	}
}

func TestEngineDoesNotSuggestAfterNonZeroPreviousOutcome(t *testing.T) {
	engine := newTestEngine(t, Config{ConfidenceThreshold: 0.01})
	observe(t, engine, Outcome{Line: "make test", CWD: "/work", ExitCode: 0})
	observe(t, engine, Outcome{Line: "make build", CWD: "/work", ExitCode: 0})
	if got := engine.Suggest("", "/work", &Previous{Line: "make test", CWD: "/work", ExitCode: 1}); got != "" {
		t.Fatalf("suggestion after nonzero previous outcome = %q", got)
	}
}

func TestEngineRequiresHashConfirmationForImportedEvidence(t *testing.T) {
	history := filepath.Join(t.TempDir(), ".zsh_history")
	if err := writeTestHistory(history, ": 1700000000:0;make test\n: 1700000001:0;make build\n: 1700000002:0;make test\n: 1700000003:0;make build\n"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.ConfidenceThreshold = 0.01
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"zsh"}
	cfg.HistoryPaths = map[string]string{"zsh": history}
	engine, err := open(context.Background(), cfg, filepath.Join(t.TempDir(), "prediction.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if err := engine.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := engine.Suggest("", "/work", &Previous{Line: "make test", CWD: "/work", ExitCode: 0}); got != "" {
		t.Fatalf("unconfirmed imported suggestion = %q", got)
	}
	observe(t, engine, Outcome{Line: "make test", CWD: "/work", ExitCode: 0})
	observe(t, engine, Outcome{Line: "make build", CWD: "/work", ExitCode: 0})
	if got := engine.Suggest("", "/work", &Previous{Line: "make test", CWD: "/work", ExitCode: 0}); got != "make build" {
		t.Fatalf("confirmed imported suggestion = %q", got)
	}
}

func TestEnginePrefixFilteringAndCWDPrecedence(t *testing.T) {
	engine := newTestEngine(t, Config{ConfidenceThreshold: 0.01})
	for i := 0; i < 3; i++ {
		observe(t, engine, Outcome{Line: "git status", CWD: "/work", ExitCode: 0})
		observe(t, engine, Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
	}
	for i := 0; i < 5; i++ {
		observe(t, engine, Outcome{Line: "git status", CWD: "/other", ExitCode: 0})
		observe(t, engine, Outcome{Line: "git push", CWD: "/other", ExitCode: 0})
	}
	if got := engine.Suggest("git p", "/other", &Previous{Line: "git status", CWD: "/other", ExitCode: 0}); got != "git push" {
		t.Fatalf("prefix/cwd suggestion = %q", got)
	}
	if got := engine.Suggest("git z", "/other", &Previous{Line: "git status", CWD: "/other", ExitCode: 0}); got != "" {
		t.Fatalf("non-matching prefix suggestion = %q", got)
	}
}

func TestEnginePersistsTransitionsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := Config{ConfidenceThreshold: 0.01}
	first, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	observe(t, first, Outcome{Line: "one", CWD: "/work", ExitCode: 0})
	observe(t, first, Outcome{Line: "two", CWD: "/work", ExitCode: 0})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if got := second.Suggest("", "/work", &Previous{Line: "one", CWD: "/work", ExitCode: 0}); got != "two" {
		t.Fatalf("persisted suggestion = %q", got)
	}
}

func TestEnginesCanOpenSameDatabaseConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := Config{ConfidenceThreshold: 0.01}
	first, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	type openResult struct {
		engine *Engine
		err    error
	}
	opened := make(chan openResult, 1)
	go func() {
		engine, openErr := open(context.Background(), cfg, path, time.Now)
		opened <- openResult{engine: engine, err: openErr}
	}()
	select {
	case result := <-opened:
		defer func() { _ = first.Close() }()
		if result.err != nil {
			t.Fatalf("second open error = %v", result.err)
		}
		defer func() { _ = result.engine.Close() }()
	case <-time.After(200 * time.Millisecond):
		_ = first.Close()
		result := <-opened
		if result.engine != nil {
			_ = result.engine.Close()
		}
		t.Fatal("second engine blocked on the first engine's database lock")
	}
}

func TestEnginesCanCreateSameDatabaseConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := Config{ConfidenceThreshold: 0.01}
	start := make(chan struct{})
	type openResult struct {
		engine *Engine
		err    error
	}
	opened := make(chan openResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			engine, err := open(context.Background(), cfg, path, time.Now)
			opened <- openResult{engine: engine, err: err}
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		result := <-opened
		if result.err != nil {
			t.Fatalf("concurrent create %d: %v", i, result.err)
		}
		_ = result.engine.Close()
	}
}

func TestEnginesCanCreateSameDatabaseAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	start := make(chan struct{})
	results := make(chan struct {
		output []byte
		err    error
	}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			cmd := exec.Command(os.Args[0], "-test.run=TestOpenStoreProcessHelper")
			cmd.Env = append(os.Environ(), "HASH_PREDICTION_OPEN_HELPER="+path)
			output, err := cmd.CombinedOutput()
			results <- struct {
				output []byte
				err    error
			}{output: output, err: err}
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("process %d open error = %v\n%s", i, result.err, result.output)
		}
	}
}

func TestOpenStoreWaitsForConcurrentInitializer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	locker, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := locker.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		st, _, openErr := openStore(path)
		if st != nil {
			_ = st.close()
		}
		opened <- openErr
	}()
	time.Sleep(100 * time.Millisecond)
	if err := locker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-opened; err != nil {
		t.Fatalf("openStore() during concurrent initialization = %v", err)
	}
}

func TestOpenStoreProcessHelper(t *testing.T) {
	path := os.Getenv("HASH_PREDICTION_OPEN_HELPER")
	if path == "" {
		return
	}
	engine, err := open(context.Background(), Config{ConfidenceThreshold: 0.01}, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEnginesCanOpenPopulatedDatabaseConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	seed, _, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]transition, 25_000)
	for i := 0; i < 25_000; i++ {
		previous := "previous-" + strconv.Itoa(i)
		next := "next-" + strconv.Itoa(i)
		values[key(previous, next, "/work")] = transition{Previous: previous, Next: next, CWD: "/work", HashCount: 1, LastUsed: time.Now()}
	}
	if err := seed.putAll(values); err != nil {
		t.Fatal(err)
	}
	_ = seed.close()

	cfg := Config{ConfidenceThreshold: 0.01}
	start := make(chan struct{})
	opened := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			engine, openErr := open(context.Background(), cfg, path, time.Now)
			if engine != nil {
				_ = engine.Close()
			}
			opened <- openErr
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-opened; err != nil {
			t.Fatalf("concurrent populated open %d: %v", i, err)
		}
	}
}

func TestEngineSeesTransitionLearnedByAnotherSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := Config{ConfidenceThreshold: 0.01}
	first, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	observe(t, first, Outcome{Line: "git status", CWD: "/work", ExitCode: 0})
	observe(t, first, Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
	if got := second.Suggest("", "/work", &Previous{Line: "git status", CWD: "/work", ExitCode: 0}); got != "git pull" {
		t.Fatalf("cross-session suggestion = %q, want %q", got, "git pull")
	}
}

func TestLocalWriteDoesNotHideAnotherSessionsTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := Config{ConfidenceThreshold: 0.01}
	first, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	observe(t, first, Outcome{Line: "external-a", CWD: "/work", ExitCode: 0})
	observe(t, first, Outcome{Line: "external-b", CWD: "/work", ExitCode: 0})
	observe(t, second, Outcome{Line: "local-a", CWD: "/work", ExitCode: 0})
	observe(t, second, Outcome{Line: "local-b", CWD: "/work", ExitCode: 0})

	if got := second.Suggest("", "/work", &Previous{Line: "external-a", CWD: "/work", ExitCode: 0}); got != "external-b" {
		t.Fatalf("external suggestion after local write = %q, want %q", got, "external-b")
	}
}

func TestConcurrentSessionsDoNotLoseTransitionCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := Config{ConfidenceThreshold: 0.6}
	first, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	for i := 0; i < 2; i++ {
		observe(t, first, Outcome{Line: "git status", CWD: "/work", ExitCode: 0})
		observe(t, first, Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
		observe(t, second, Outcome{Line: "git status", CWD: "/work", ExitCode: 0})
		observe(t, second, Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
	}
	observer, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	if got := observer.Suggest("", "/work", &Previous{Line: "git status", CWD: "/work", ExitCode: 0}); got != "git pull" {
		t.Fatalf("combined cross-session suggestion = %q, want %q", got, "git pull")
	}
}

func TestConcurrentSessionWritesAreAllPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := Config{ConfidenceThreshold: 0.01}
	engines := make([]*Engine, 2)
	for i := range engines {
		engine, err := open(context.Background(), cfg, path, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		engines[i] = engine
		defer func() { _ = engine.Close() }()
	}

	const writesPerSession = 1
	start := make(chan struct{})
	errs := make(chan error, len(engines)*writesPerSession*3)
	var wg sync.WaitGroup
	for _, engine := range engines {
		wg.Add(1)
		go func(engine *Engine) {
			defer wg.Done()
			<-start
			for i := 0; i < writesPerSession; i++ {
				errs <- engine.Observe(Outcome{Line: "build", CWD: "/work", ExitCode: 0})
				errs <- engine.Observe(Outcome{Line: "test", CWD: "/work", ExitCode: 0})
				errs <- engine.Observe(Outcome{Line: "reset", CWD: "/work", ExitCode: 1, FailureKind: "exit_status"})
			}
		}(engine)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Observe() error = %v", err)
		}
	}

	stored, values, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stored.close() }()
	got := values[key("build", "test", "/work")].HashCount
	if want := len(engines) * writesPerSession; got != want {
		t.Fatalf("persisted hash count = %d, want %d", got, want)
	}
}

func TestObserveRetriesStorageContentionWithoutLosingTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	engine, err := open(context.Background(), Config{ConfidenceThreshold: 0.01}, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	if err := engine.Observe(Outcome{Line: "build", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}

	locker, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Observe(Outcome{Line: "test", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatalf("Observe() did not queue transient contention: %v", err)
	}
	if err := locker.Close(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		stored, values, openErr := openStore(path)
		if openErr == nil {
			_ = stored.close()
			if values[key("build", "test", "/work")].HashCount == 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("queued transition was not persisted after contention cleared")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCloseDoesNotDuplicateQueuedTransition(t *testing.T) {
	for i := 0; i < 25; i++ {
		path := filepath.Join(t.TempDir(), "prediction.db")
		engine, err := open(context.Background(), Config{ConfidenceThreshold: 0.01}, path, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		observe(t, engine, Outcome{Line: "build", CWD: "/work", ExitCode: 0})
		locker, err := bbolt.Open(path, 0o600, nil)
		if err != nil {
			t.Fatal(err)
		}
		observe(t, engine, Outcome{Line: "test", CWD: "/work", ExitCode: 0})
		if err := locker.Close(); err != nil {
			t.Fatal(err)
		}
		if err := engine.Close(); err != nil {
			t.Fatal(err)
		}
		stored, values, err := openStore(path)
		if err != nil {
			t.Fatal(err)
		}
		_ = stored.close()
		if got := values[key("build", "test", "/work")].HashCount; got != 1 {
			t.Fatalf("iteration %d persisted count = %d, want 1", i, got)
		}
	}
}

func TestDelayedIncrementDoesNotRegressLastUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	st, _, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.close() }()
	newer := time.Now()
	older := newer.Add(-time.Hour)
	k := key("build", "test", "/work")
	if _, _, err := st.increment(k, transition{Previous: "build", Next: "test", CWD: "/work", LastUsed: newer}, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.increment(k, transition{Previous: "build", Next: "test", CWD: "/work", LastUsed: older}, true); err != nil {
		t.Fatal(err)
	}
	_, values, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := values[k].LastUsed; !got.Equal(newer) {
		t.Fatalf("LastUsed = %v, want %v", got, newer)
	}
}

func TestStaleRefreshCannotOverwriteLocalObservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := Config{ConfidenceThreshold: 0.01}
	first, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	if err := first.Observe(Outcome{Line: "external-a", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if err := first.Observe(Outcome{Line: "external-b", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := second.store.refresh()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil {
		t.Fatal("refresh() returned no changed snapshot")
	}
	if err := second.Observe(Outcome{Line: "local-a", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if err := second.Observe(Outcome{Line: "local-b", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}

	if second.applyRefresh(second.store, snapshot) {
		t.Fatal("stale refresh snapshot was applied after a newer local write")
	}
	second.mu.RLock()
	_, retained := second.values[key("local-a", "local-b", "/work")]
	second.mu.RUnlock()
	if !retained {
		t.Fatal("stale refresh discarded the local observation")
	}
}

func TestEngineCreatesPrivateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "prediction.db")
	engine, err := open(context.Background(), Config{ConfidenceThreshold: 0.01}, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v, err=%v", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("database directory mode = %v, err=%v", info.Mode().Perm(), err)
	}
}

func TestCanceledBootstrapLeavesFinalDatabaseAbsent(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "history")
	if err := os.WriteFile(history, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "prediction.db")
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := DefaultConfig()
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"bash"}
	cfg.HistoryPaths = map[string]string{"bash": history}
	engine, err := open(cancelCtx, cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_ = engine.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("canceled bootstrap created final database: %v", err)
	}
}

func TestBootstrapPromotionPreservesTemporaryDatabaseOnLinkFailure(t *testing.T) {
	root := t.TempDir()
	temporary := filepath.Join(root, "prediction.db.importing")
	final := filepath.Join(root, "prediction.db")
	st, _, err := openStore(temporary)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]transition{
		key("one", "two", ""): {Previous: "one", Next: "two", ImportedCount: 1, LastUsed: time.Now()},
	}
	if err := st.putAll(values); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("link unavailable")
	_, _, err = promoteBootstrap(temporary, final, values, func(string, string) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("promoteBootstrap() error = %v, want %v", err, sentinel)
	}
	if _, err := os.Stat(temporary); err != nil {
		t.Fatalf("temporary database was not preserved: %v", err)
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("failed promotion created final database: %v", err)
	}
}

func TestBootstrapPromotionPreservesTemporaryDatabaseWhenExistingDestinationIsInvalid(t *testing.T) {
	root := t.TempDir()
	temporary := filepath.Join(root, "prediction.db.importing")
	final := filepath.Join(root, "prediction.db")
	st, _, err := openStore(temporary)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]transition{
		key("one", "two", ""): {Previous: "one", Next: "two", ImportedCount: 1, LastUsed: time.Now()},
	}
	if err := st.putAll(values); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("not a bolt database"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err = promoteBootstrap(temporary, final, values, func(string, string) error { return fs.ErrExist })
	if err == nil {
		t.Fatal("promoteBootstrap() succeeded with an invalid existing destination")
	}
	if _, err := os.Stat(temporary); err != nil {
		t.Fatalf("temporary database was not preserved: %v", err)
	}
}

func TestWaitReadyReturnsBootstrapFailure(t *testing.T) {
	sentinel := errors.New("bootstrap failed")
	engine := &Engine{ready: make(chan struct{}), readyErr: sentinel}
	close(engine.ready)
	if err := engine.WaitReady(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("WaitReady() error = %v, want %v", err, sentinel)
	}
}

func TestBootstrapFailureIsActionableAndPreservedOnClose(t *testing.T) {
	temporary := filepath.Join(t.TempDir(), "prediction.db.importing-recovery")
	st, values, err := openStore(temporary)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("promotion failed")
	engine := &Engine{
		ready:             make(chan struct{}),
		readyErr:          sentinel,
		preserveBootstrap: true,
		bootstrapPath:     temporary,
		store:             st,
		values:            values,
	}
	close(engine.ready)
	if err := engine.Observe(Outcome{Line: "build", CWD: "/work", ExitCode: 0}); !errors.Is(err, sentinel) {
		t.Fatalf("Observe() error = %v, want bootstrap failure", err)
	}
	if err := engine.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("Close() error = %v, want bootstrap failure", err)
	}
	if _, err := os.Stat(temporary); err != nil {
		t.Fatalf("Close() removed recoverable bootstrap database: %v", err)
	}
}

func TestConcurrentFirstImportProducesSharedUsableDatabase(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "history")
	if err := writeTestHistory(history, strings.Repeat("one\ntwo\n", 10_000)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "prediction.db")
	cfg := DefaultConfig()
	cfg.ConfidenceThreshold = 0.01
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"bash"}
	cfg.HistoryPaths = map[string]string{"bash": history}
	type openResult struct {
		engine *Engine
		err    error
	}
	start := make(chan struct{})
	opened := make(chan openResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			engine, err := open(context.Background(), cfg, path, time.Now)
			opened <- openResult{engine: engine, err: err}
		}()
	}
	close(start)
	engines := make([]*Engine, 0, 2)
	for i := 0; i < 2; i++ {
		result := <-opened
		if result.err != nil {
			t.Fatalf("open %d: %v", i, result.err)
		}
		engines = append(engines, result.engine)
		defer func() { _ = result.engine.Close() }()
	}
	readyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, engine := range engines {
		if err := engine.WaitReady(readyCtx); err != nil {
			t.Fatalf("engine %d ready: %v", i, err)
		}
	}
	observe(t, engines[0], Outcome{Line: "one", CWD: "/work", ExitCode: 0})
	observe(t, engines[0], Outcome{Line: "two", CWD: "/work", ExitCode: 0})
	if got := engines[1].Suggest("", "/work", &Previous{Line: "one", CWD: "/work", ExitCode: 0}); got != "two" {
		t.Fatalf("second engine suggestion after concurrent import = %q, want %q", got, "two")
	}
}

func TestSuggestDuringBootstrapIsRaceFree(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "history")
	if err := writeTestHistory(history, strings.Repeat("one\ntwo\n", 20_000)); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.ConfidenceThreshold = 0.01
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"bash"}
	cfg.HistoryPaths = map[string]string{"bash": history}
	engine, err := open(context.Background(), cfg, filepath.Join(root, "prediction.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = engine.Suggest("", "/work", &Previous{Line: "one", CWD: "/work", ExitCode: 0})
			}
		}
	}()
	readyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := engine.WaitReady(readyCtx); err != nil {
		cancel()
		close(stop)
		wg.Wait()
		t.Fatal(err)
	}
	cancel()
	close(stop)
	wg.Wait()
}

func newTestEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prediction.db")
	engine, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func observe(t *testing.T, engine *Engine, outcome Outcome) {
	t.Helper()
	if err := engine.Observe(outcome); err != nil {
		t.Fatalf("Observe(%q) error = %v", outcome.Line, err)
	}
}
