package autosuggestion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestReadHistoryParsesBashExtendedZshAndFishNewestFirst(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		shell, contents string
		want            []string
	}{
		{"bash", "#1700000000\ngit old\n#1700000001\ngit new\n", []string{"git new", "git old"}},
		{"zsh", ": 1700000000:0;git old\n: 1700000001:0;git new\n", []string{"git new", "git old"}},
		{"fish", "- cmd: git old\n  when: 1700000000\n- cmd: \"git new\"\n  when: 1700000001\n", []string{"git new", "git old"}},
	}
	for _, tc := range tests {
		t.Run(tc.shell, func(t *testing.T) {
			path := filepath.Join(root, tc.shell)
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readHistory(context.Background(), path, tc.shell, 100)
			if err != nil || strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("readHistory() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestReadHistoryUsesBoundedTailAndCommandLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	contents := strings.Repeat("x", int(maxHistoryBytes)+100) + "\nfirst\nsecond\nthird\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readHistory(context.Background(), path, "bash", 2)
	if err != nil || strings.Join(got, ",") != "third,second" {
		t.Fatalf("readHistory() = %q, %v", got, err)
	}
}

func TestCacheOneTimePersistencePrivacyAndCorruption(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "zsh_history")
	if err := os.WriteFile(history, []byte(": 1:0;git old\n: 2:0;git first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"zsh"}
	cfg.HistoryPaths = map[string]string{"zsh": history}
	path := filepath.Join(root, "plugin-data", "history.db")
	cache, err := OpenCache(context.Background(), cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := cache.Suggest("git", "bash"); got != "git first" {
		t.Fatalf("first suggestion = %q", got)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v, err=%v", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v, err=%v", info.Mode().Perm(), err)
	}
	if err := os.WriteFile(history, []byte(": 3:0;git later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenCache(context.Background(), cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Suggest("git", "bash"); got != "git first" {
		t.Fatalf("one-time suggestion = %q", got)
	}

	corrupt := filepath.Join(root, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not bbolt"), 0o600); err != nil {
		t.Fatal(err)
	}
	degraded, err := OpenCache(context.Background(), cfg, corrupt)
	if err != nil {
		t.Fatalf("corrupt cache should degrade, got %v", err)
	}
	defer degraded.Close()
	if got := degraded.Suggest("git", "bash"); got != "" {
		t.Fatalf("corrupt cache suggestion = %q", got)
	}
}

func TestCacheDisabledHasNoFilesystemSideEffects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "history.db")
	cache, err := OpenCache(context.Background(), DefaultConfig(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("disabled cache created or read storage: %v", err)
	}
}

func TestConcurrentFirstCachesConvergeWithoutTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "history")
	if err := os.WriteFile(history, []byte("git old\ngit winner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"bash"}
	cfg.HistoryPaths = map[string]string{"bash": history}
	path := filepath.Join(root, "data", "history.db")
	var wg sync.WaitGroup
	caches := make([]*Cache, 2)
	errs := make([]error, 2)
	for i := range caches {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			caches[i], errs[i] = OpenCache(context.Background(), cfg, path)
			if errs[i] == nil {
				errs[i] = caches[i].WaitReady(context.Background())
			}
		}(i)
	}
	wg.Wait()
	for i := range caches {
		if errs[i] != nil {
			t.Fatalf("cache %d: %v", i, errs[i])
		}
		if got := caches[i].Suggest("git", "bash"); got != "git winner" {
			t.Fatalf("cache %d suggestion = %q", i, got)
		}
		_ = caches[i].Close()
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".history.db.importing-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary caches = %v, err=%v", matches, err)
	}
}

func TestCanceledImportLeavesNoFinalOrTemporaryCache(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "history")
	if err := os.WriteFile(history, []byte("git old\ngit new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"bash"}
	cfg.HistoryPaths = map[string]string{"bash": history}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(root, "data", "history.db")
	cache, err := OpenCache(ctx, cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	_ = cache.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("final cache exists: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".history.db.importing-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary caches remain: %v", matches)
	}
}

func TestCacheImportsMatchingCommandOlderThanLiveHistoryLimit(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "history")
	var contents strings.Builder
	contents.WriteString("git matching-older\n")
	for i := 0; i < 101; i++ {
		contents.WriteString("echo newer-")
		contents.WriteString(stringOf('x', i+1))
		contents.WriteByte('\n')
	}
	if err := os.WriteFile(history, []byte(contents.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.HistoryLimit = 100
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"bash"}
	cfg.HistoryPaths = map[string]string{"bash": history}
	cache, err := OpenCache(context.Background(), cfg, filepath.Join(root, "data", "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if err := cache.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := cache.Suggest("git", "bash"); got != "git matching-older" {
		t.Fatalf("older imported suggestion = %q", got)
	}
}

func TestExistingCachePermissionsAreRepairedAndSymlinksRejected(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "history")
	if err := os.WriteFile(history, []byte("git cached\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"bash"}
	cfg.HistoryPaths = map[string]string{"bash": history}
	path := filepath.Join(root, "data", "history.db")
	cache, err := OpenCache(context.Background(), cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = cache.Close()
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenCache(context.Background(), cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("repaired database mode = %v, err=%v", info, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("repaired directory mode = %v, err=%v", info, err)
	}

	target := filepath.Join(root, "target.db")
	if err := os.Link(path, target); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "symlink.db")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	linked, err := OpenCache(context.Background(), cfg, symlink)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	if got := linked.Suggest("git", "bash"); got != "" {
		t.Fatalf("symlink cache was loaded: %q", got)
	}
}

func TestReadHistoryRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := readHistory(ctx, path, "bash", 100); err == nil {
		t.Fatal("FIFO history was accepted")
	}
	if time.Since(started) > 50*time.Millisecond {
		t.Fatal("FIFO history read blocked")
	}
}

func TestInFlightCanceledImportLeavesNoCache(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data", "history.db")
	cfg := DefaultConfig()
	cfg.LearnFromOtherShells = true
	cfg.Shells = []string{"bash"}
	started := make(chan struct{})
	reader := func(ctx context.Context, _, _ string, _ int) ([]string, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cache, err := openCache(context.Background(), cfg, path, reader)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final cache exists: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".history.db.importing-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary caches remain: %v", matches)
	}
}

func TestDefaultDataPathRejectsRelativeXDGRoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "relative-data")
	if _, err := DefaultDataPath(); err == nil {
		t.Fatal("relative XDG_DATA_HOME was accepted")
	}
}

func TestTildeHistoryPathIsSkippedWithoutAbsoluteHome(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := expandPath("~/history"); err == nil {
		t.Fatal("tilde history path resolved without HOME")
	}
}
