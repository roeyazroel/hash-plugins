package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/roeyazroel/hash-plugins/internal/prediction"
	"go.etcd.io/bbolt"
)

func TestInitializeRespondsBeforeDelayedStorageOpen(t *testing.T) {
	hostToPlugin, writeRequest := io.Pipe()
	pluginToHost, readResponse := io.Pipe()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	state := newPluginState(func(ctx context.Context, cfg prediction.Config, path string) (*prediction.Engine, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return prediction.Open(ctx, cfg, path)
	})
	server := newServer(hostToPlugin, readResponse, state)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	t.Cleanup(func() {
		_ = writeRequest.Close()
		_ = readResponse.Close()
		releaseOnce.Do(func() { close(release) })
		if err := state.close(); err != nil {
			t.Error(err)
		}
		select {
		case err := <-serveDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("plugin server did not stop")
		}
	})

	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocol_version": 1,
			"settings":         map[string]any{},
			"session_kind":     "interactive",
		},
	}
	if err := json.NewEncoder(writeRequest).Encode(request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("storage open did not start")
	}
	if state.get() != nil {
		t.Fatal("engine was available before storage opening completed")
	}

	response := make(chan map[string]any, 1)
	go func() {
		line, err := bufio.NewReader(pluginToHost).ReadBytes('\n')
		if err != nil {
			return
		}
		var value map[string]any
		if json.Unmarshal(line, &value) == nil {
			response <- value
		}
	}()
	select {
	case value := <-response:
		if value["id"] != float64(1) || value["error"] != nil {
			t.Fatalf("initialize response = %#v", value)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("initialize waited for delayed storage opening")
	}

	releaseOnce.Do(func() { close(release) })
	deadline := time.After(time.Second)
	for state.get() == nil {
		select {
		case <-deadline:
			t.Fatal("engine was not installed after storage opening completed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestPluginStateShutdownClosesLateCanceledOpenerEngine(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	path := filepath.Join(t.TempDir(), "prediction.db")
	opened := make(chan struct{})
	state := newPluginState(func(ctx context.Context, cfg prediction.Config, path string) (*prediction.Engine, error) {
		close(started)
		<-release
		engine, err := prediction.Open(ctx, cfg, path)
		close(opened)
		return engine, err
	})
	state.start(prediction.Config{ConfidenceThreshold: 0.01}, path)
	<-started
	closed := make(chan error, 1)
	go func() { closed <- state.close() }()
	close(release)
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("delayed opener did not finish")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wait for delayed opener")
	}
	if state.get() != nil {
		t.Fatal("canceled storage opener installed an engine after shutdown")
	}
}

func TestPluginStateShutdownWaitsForReinitializationAfterPreviousClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := prediction.Config{ConfidenceThreshold: 0.01}
	first, err := prediction.Open(context.Background(), cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	var opens int
	secondCanceled := make(chan struct{})
	state := newPluginState(func(ctx context.Context, _ prediction.Config, _ string) (*prediction.Engine, error) {
		opens++
		if opens == 1 {
			return first, nil
		}
		<-ctx.Done()
		close(secondCanceled)
		return nil, ctx.Err()
	})
	t.Cleanup(func() {
		if err := state.close(); err != nil {
			t.Error(err)
		}
	})

	state.start(cfg, path)
	deadline := time.After(time.Second)
	for state.get() == nil {
		select {
		case <-deadline:
			t.Fatal("first initialization did not install its engine")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := first.Observe(prediction.Outcome{Line: "build", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	locker, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Observe(prediction.Outcome{Line: "test", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}

	secondStartDone := make(chan struct{})
	go func() {
		state.start(cfg, path)
		close(secondStartDone)
	}()
	select {
	case <-secondStartDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second initialization waited for the previous engine close")
	}

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- state.close() }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before reinitialization completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := locker.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-secondCanceled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the reinitialization opener")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wait for the reinitialization opener")
	}
}

func TestPluginStateShutdownReturnsRetiredEngineCloseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prediction.db")
	cfg := prediction.Config{ConfidenceThreshold: 0.01}
	first, err := prediction.Open(context.Background(), cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	var opens int
	state := newPluginState(func(ctx context.Context, _ prediction.Config, _ string) (*prediction.Engine, error) {
		opens++
		if opens == 1 {
			return first, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})

	state.start(cfg, path)
	deadline := time.After(time.Second)
	for state.get() == nil {
		select {
		case <-deadline:
			t.Fatal("first initialization did not install its engine")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := first.Observe(prediction.Outcome{Line: "build", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	locker, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	if err := first.Observe(prediction.Outcome{Line: "test", CWD: "/work", ExitCode: 0}); err != nil {
		t.Fatalf("Observe() did not queue transient contention: %v", err)
	}

	state.start(cfg, path)
	err = state.close()
	if !errors.Is(err, bbolt.ErrTimeout) {
		t.Fatalf("shutdown error = %v, want retired engine close timeout", err)
	}
	if err := state.close(); err != nil {
		t.Fatalf("second shutdown error = %v, want consumed retirement error", err)
	}
}
