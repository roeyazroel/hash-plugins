package prediction

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.etcd.io/bbolt"
	bbolterrors "go.etcd.io/bbolt/errors"
)

var bucket = []byte("transitions")

type store struct {
	path       string
	mu         sync.Mutex
	revision   int
	generation uint64
}

const (
	storeOpenLockTimeout = 400 * time.Millisecond
	storeOpenRetryDelay  = 25 * time.Millisecond
	storeReadLockTimeout = 50 * time.Millisecond
	// Leave half of command.finished's 150 ms budget for protocol handling;
	// writes that remain contended move to the retry queue.
	storeWriteLockTimeout = 75 * time.Millisecond
	bootstrapLockTimeout  = 5 * time.Second
)

type encodedTransition struct {
	key   string
	value []byte
}

type refreshSnapshot struct {
	values                 map[string]transition
	baseRevision, revision int
	baseGeneration         uint64
}

func openStore(path string) (*store, map[string]transition, error) {
	return openStoreContext(context.Background(), path)
}

func openBootstrapStore(path string) (*store, map[string]transition, error) {
	return openBootstrapStoreContext(context.Background(), path)
}

func openStoreContext(ctx context.Context, path string) (*store, map[string]transition, error) {
	return openStoreWithTimeoutContext(ctx, path, storeOpenLockTimeout)
}

func openBootstrapStoreContext(ctx context.Context, path string) (*store, map[string]transition, error) {
	return openStoreWithTimeoutContext(ctx, path, bootstrapLockTimeout)
}

func openStoreWithTimeoutContext(ctx context.Context, path string, timeout time.Duration) (*store, map[string]transition, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	info, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, nil, statErr
	}
	initialize := os.IsNotExist(statErr)
	if statErr == nil {
		initialize = info.Size() == 0
	}
	lockTimeout := timeout
	if initialize && lockTimeout < bootstrapLockTimeout {
		lockTimeout = bootstrapLockTimeout
	}
	db, err := openBoltContext(ctx, path, !initialize, lockTimeout)
	if err != nil {
		return nil, nil, err
	}
	_ = os.Chmod(path, 0o600)
	var encoded []encodedTransition
	revision := 0
	if initialize {
		err = db.Update(func(tx *bbolt.Tx) error {
			revision = tx.ID()
			b, err := tx.CreateBucketIfNotExists(bucket)
			if err != nil {
				return err
			}
			encoded, err = copyBucket(b)
			return err
		})
	} else {
		err = db.View(func(tx *bbolt.Tx) error {
			revision = tx.ID()
			encoded, err = copyBucket(tx.Bucket(bucket))
			return err
		})
	}
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if err := db.Close(); err != nil {
		return nil, nil, err
	}
	values, err := decodeTransitions(encoded)
	if err != nil {
		return nil, nil, err
	}
	return &store{path: path, revision: revision}, values, nil
}

func openBoltContext(ctx context.Context, path string, readOnly bool, timeout time.Duration) (*bbolt.DB, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, lastErr
		}
		attempt := storeOpenRetryDelay
		if remaining < attempt {
			attempt = remaining
		}
		db, err := bbolt.Open(path, 0o600, &bbolt.Options{ReadOnly: readOnly, Timeout: attempt})
		if err == nil {
			return db, nil
		}
		if !isStoreContention(err) {
			return nil, err
		}
		lastErr = err
	}
}

func copyBucket(b *bbolt.Bucket) ([]encodedTransition, error) {
	encoded := []encodedTransition{}
	if b == nil {
		return encoded, nil
	}
	err := b.ForEach(func(k, v []byte) error {
		if v == nil {
			return nil
		}
		encoded = append(encoded, encodedTransition{key: string(k), value: append([]byte(nil), v...)})
		return nil
	})
	return encoded, err
}

func decodeTransitions(encoded []encodedTransition) (map[string]transition, error) {
	values := make(map[string]transition, len(encoded))
	for _, item := range encoded {
		var t transition
		if err := json.Unmarshal(item.value, &t); err != nil {
			return nil, err
		}
		values[item.key] = t
	}
	return values, nil
}

func (s *store) increment(key string, next transition, hash bool) (transition, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := bbolt.Open(s.path, 0o600, &bbolt.Options{Timeout: storeWriteLockTimeout})
	if err != nil {
		return transition{}, 0, err
	}
	defer func() { _ = db.Close() }()
	err = db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		if raw := b.Get([]byte(key)); len(raw) > 0 {
			var current transition
			if err := json.Unmarshal(raw, &current); err != nil {
				return err
			}
			next.HashCount = current.HashCount
			next.ImportedCount = current.ImportedCount
			if current.LastUsed.After(next.LastUsed) {
				next.LastUsed = current.LastUsed
			}
		}
		if hash {
			next.HashCount++
		} else {
			next.ImportedCount++
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), raw)
	})
	if err != nil {
		return transition{}, 0, err
	}
	s.generation++
	return next, s.generation, nil
}

func (s *store) hasGeneration(generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation == generation
}

func (s *store) load() (map[string]transition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := bbolt.Open(s.path, 0o600, &bbolt.Options{ReadOnly: true, Timeout: storeReadLockTimeout})
	if err != nil {
		return nil, err
	}
	var encoded []encodedTransition
	revision := 0
	err = db.View(func(tx *bbolt.Tx) error {
		revision = tx.ID()
		encoded, err = copyBucket(tx.Bucket(bucket))
		return err
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	values, err := decodeTransitions(encoded)
	if err != nil {
		return nil, err
	}
	s.revision = revision
	s.generation++
	return values, nil
}

func (s *store) refresh() (*refreshSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := bbolt.Open(s.path, 0o600, &bbolt.Options{ReadOnly: true, Timeout: storeReadLockTimeout})
	if err != nil {
		return nil, err
	}
	var encoded []encodedTransition
	changed := false
	baseRevision := s.revision
	baseGeneration := s.generation
	revision := baseRevision
	err = db.View(func(tx *bbolt.Tx) error {
		revision = tx.ID()
		if revision == baseRevision {
			return nil
		}
		changed = true
		encoded, err = copyBucket(tx.Bucket(bucket))
		return err
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	values, err := decodeTransitions(encoded)
	if err != nil {
		return nil, err
	}
	return &refreshSnapshot{values: values, baseRevision: baseRevision, revision: revision, baseGeneration: baseGeneration}, nil
}

func (s *store) accept(snapshot *refreshSnapshot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot == nil || s.revision != snapshot.baseRevision || s.generation != snapshot.baseGeneration {
		return false
	}
	s.revision = snapshot.revision
	return true
}

func (s *store) putAll(values map[string]transition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := bbolt.Open(s.path, 0o600, &bbolt.Options{Timeout: storeWriteLockTimeout})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	revision := 0
	err = db.Update(func(tx *bbolt.Tx) error {
		revision = tx.ID()
		b, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		for k, t := range values {
			raw, e := json.Marshal(t)
			if e != nil {
				return e
			}
			if e = b.Put([]byte(k), raw); e != nil {
				return e
			}
		}
		return nil
	})
	if err == nil {
		s.revision = revision
		s.generation++
	}
	return err
}

func (s *store) mergeAll(values map[string]transition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := bbolt.Open(s.path, 0o600, &bbolt.Options{Timeout: bootstrapLockTimeout})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	revision := 0
	err = db.Update(func(tx *bbolt.Tx) error {
		revision = tx.ID()
		b, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		for k, incoming := range values {
			merged := incoming
			if raw := b.Get([]byte(k)); len(raw) > 0 {
				if err := json.Unmarshal(raw, &merged); err != nil {
					return err
				}
				merged.HashCount += incoming.HashCount
				if incoming.ImportedCount > merged.ImportedCount {
					merged.ImportedCount = incoming.ImportedCount
				}
				if incoming.LastUsed.After(merged.LastUsed) {
					merged.LastUsed = incoming.LastUsed
				}
			}
			raw, err := json.Marshal(merged)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(k), raw); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.revision = revision
		s.generation++
	}
	return err
}

func (s *store) close() error { return nil }

func isStoreContention(err error) bool { return errors.Is(err, bbolterrors.ErrTimeout) }
