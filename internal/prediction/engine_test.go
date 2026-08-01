package prediction

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEngineLearnsOnlyAdjacentSuccessfulTransitions(t *testing.T) {
	engine := newTestEngine(t, Config{ConfidenceThreshold: 0.01})

	engine.Observe(Outcome{Line: "git status", CWD: "/work", ExitCode: 1, FailureKind: "exit_status"})
	engine.Observe(Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
	if got := engine.Suggest("", "/work", &Previous{Line: "git pull", CWD: "/work", ExitCode: 0}); got != "" {
		t.Fatalf("suggestion after failed predecessor = %q", got)
	}

	engine.Observe(Outcome{Line: "git status", CWD: "/work", ExitCode: 0})
	engine.Observe(Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
	if got := engine.Suggest("", "/work", &Previous{Line: "git status", CWD: "/work", ExitCode: 0}); got != "git pull" {
		t.Fatalf("suggestion after successful transition = %q", got)
	}
}

func TestEngineDoesNotSuggestAfterNonZeroPreviousOutcome(t *testing.T) {
	engine := newTestEngine(t, Config{ConfidenceThreshold: 0.01})
	engine.Observe(Outcome{Line: "make test", CWD: "/work", ExitCode: 0})
	engine.Observe(Outcome{Line: "make build", CWD: "/work", ExitCode: 0})
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
	engine.Observe(Outcome{Line: "make test", CWD: "/work", ExitCode: 0})
	engine.Observe(Outcome{Line: "make build", CWD: "/work", ExitCode: 0})
	if got := engine.Suggest("", "/work", &Previous{Line: "make test", CWD: "/work", ExitCode: 0}); got != "make build" {
		t.Fatalf("confirmed imported suggestion = %q", got)
	}
}

func TestEnginePrefixFilteringAndCWDPrecedence(t *testing.T) {
	engine := newTestEngine(t, Config{ConfidenceThreshold: 0.01})
	for i := 0; i < 3; i++ {
		engine.Observe(Outcome{Line: "git status", CWD: "/work", ExitCode: 0})
		engine.Observe(Outcome{Line: "git pull", CWD: "/work", ExitCode: 0})
	}
	for i := 0; i < 5; i++ {
		engine.Observe(Outcome{Line: "git status", CWD: "/other", ExitCode: 0})
		engine.Observe(Outcome{Line: "git push", CWD: "/other", ExitCode: 0})
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
	first.Observe(Outcome{Line: "one", CWD: "/work", ExitCode: 0})
	first.Observe(Outcome{Line: "two", CWD: "/work", ExitCode: 0})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := open(context.Background(), cfg, path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got := second.Suggest("", "/work", &Previous{Line: "one", CWD: "/work", ExitCode: 0}); got != "two" {
		t.Fatalf("persisted suggestion = %q", got)
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
