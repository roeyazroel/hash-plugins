package autosuggestion

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.etcd.io/bbolt"
)

var historyBucket = []byte("history")

type Cache struct {
	mu       sync.RWMutex
	shells   []string
	entries  map[string][]string
	ready    chan struct{}
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	scanHook func(context.Context)
}

func OpenCache(ctx context.Context, cfg Config, path string) (*Cache, error) {
	return openCache(ctx, cfg, path, readHistory)
}

func openCache(ctx context.Context, cfg Config, path string, reader historyReader) (*Cache, error) {
	cache := &Cache{shells: append([]string(nil), cfg.Shells...), entries: map[string][]string{}, ready: make(chan struct{})}
	if !cfg.LearnFromOtherShells || ctx.Err() != nil {
		close(cache.ready)
		return cache, nil
	}
	if _, err := os.Lstat(path); err == nil {
		entries, loadErr := loadPrivateCache(path)
		if loadErr == nil {
			cache.entries = entries
		}
		close(cache.ready)
		return cache, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		close(cache.ready)
		return cache, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := makeDirectoryPrivate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	bootstrapCtx, cancel := context.WithCancel(ctx)
	cache.cancel = cancel
	cache.wg.Add(1)
	go cache.bootstrap(bootstrapCtx, cfg, path, reader)
	return cache, nil
}

func (c *Cache) bootstrap(ctx context.Context, cfg Config, path string, reader historyReader) {
	defer c.wg.Done()
	defer close(c.ready)
	if ctx.Err() != nil {
		return
	}
	paths := defaultHistoryPaths(cfg.Shells)
	for shell, configured := range cfg.HistoryPaths {
		paths[shell] = configured
	}
	entries := make(map[string][]string, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		if ctx.Err() != nil {
			return
		}
		commands, err := reader(ctx, paths[shell], shell, maxHistoryCommands)
		if err != nil {
			continue
		}
		for _, command := range commands {
			if safeStoredCommand(command) {
				entries[shell] = append(entries[shell], command)
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	winner, err := promoteCache(ctx, path, entries)
	if err != nil {
		return
	}
	loaded, err := loadCache(winner)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.entries = loaded
	c.mu.Unlock()
}

func promoteCache(ctx context.Context, path string, entries map[string][]string) (string, error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".history.db.importing-*")
	if err != nil {
		return "", err
	}
	tempPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := os.Remove(tempPath); err != nil {
		return "", err
	}
	defer os.Remove(tempPath)
	if err := writeCache(tempPath, entries); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err := os.Link(tempPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return path, nil
		}
		return "", err
	}
	if ctx.Err() != nil {
		if tempInfo, tempErr := os.Stat(tempPath); tempErr == nil {
			if finalInfo, finalErr := os.Stat(path); finalErr == nil && os.SameFile(tempInfo, finalInfo) {
				_ = os.Remove(path)
			}
		}
		return "", ctx.Err()
	}
	return path, nil
}

func writeCache(path string, entries map[string][]string) error {
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(historyBucket)
		if err != nil {
			return err
		}
		for shell, commands := range entries {
			raw, err := json.Marshal(commands)
			if err != nil {
				return err
			}
			if err := bucket.Put([]byte(shell), raw); err != nil {
				return err
			}
		}
		return nil
	})
	closeErr := db.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(path, 0o600)
}

func loadCache(path string) (map[string][]string, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer db.Close()
	entries := map[string][]string{}
	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(historyBucket)
		if bucket == nil {
			return errors.New("history bucket is missing")
		}
		return bucket.ForEach(func(key, value []byte) error {
			var commands []string
			if err := json.Unmarshal(value, &commands); err != nil {
				return err
			}
			entries[string(key)] = commands
			return nil
		})
	})
	return entries, err
}

func loadPrivateCache(path string) (map[string][]string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, os.ErrInvalid
	}
	if err := makeDirectoryPrivate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return loadCache(path)
}

func makeDirectoryPrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return os.ErrInvalid
	}
	return os.Chmod(path, 0o700)
}

func (c *Cache) WaitReady(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Cache) Suggest(prefix, dialect string) string {
	suggestion, _ := c.SuggestContext(context.Background(), prefix, dialect)
	return suggestion
}

func (c *Cache) SuggestContext(ctx context.Context, prefix, dialect string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return scanImported(ctx, c.shells, c.entries, c.scanHook, prefix, dialect)
}

func scanImported(ctx context.Context, shells []string, entries map[string][]string, hook func(context.Context), prefix, dialect string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, shell := range shells {
		for index, command := range entries[shell] {
			if index%64 == 0 {
				if hook != nil {
					hook(ctx)
				}
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			if command == prefix || !strings.HasPrefix(command, prefix) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			valid := validCandidate(prefix, command, dialect)
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if valid {
				return command, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func (c *Cache) Close() error {
	if c == nil {
		return nil
	}
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	return nil
}

func DefaultDataPath() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		return "", os.ErrInvalid
	}
	return filepath.Join(base, "hash", "plugin-data", "io.runhash.autosuggestions", "history.db"), nil
}
