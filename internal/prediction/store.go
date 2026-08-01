package prediction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"go.etcd.io/bbolt"
)

var bucket = []byte("transitions")

type store struct {
	db *bbolt.DB
	mu sync.Mutex
}

func openStore(path string) (*store, map[string]transition, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, nil, err
	}
	_ = os.Chmod(path, 0o600)
	values := map[string]transition{}
	err = db.Update(func(tx *bbolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists(bucket)
		if e != nil {
			return e
		}
		return b.ForEach(func(k, v []byte) error {
			var t transition
			if e := json.Unmarshal(v, &t); e != nil {
				return e
			}
			values[string(k)] = t
			return nil
		})
	})
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return &store{db: db}, values, nil
}

func (s *store) put(key string, t transition) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error { return tx.Bucket(bucket).Put([]byte(key), raw) })
}
func (s *store) putAll(values map[string]transition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucket)
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
}
func (s *store) close() error { s.mu.Lock(); defer s.mu.Unlock(); return s.db.Close() }
