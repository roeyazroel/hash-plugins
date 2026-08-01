package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type peerHarness struct {
	server     *Server
	toServer   *io.PipeWriter
	fromServer *bufio.Reader
	done       chan error
}

func newPeerHarness(t *testing.T) *peerHarness {
	t.Helper()
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	h := &peerHarness{
		server:     New(hostToPluginR, pluginToHostW),
		toServer:   hostToPluginW,
		fromServer: bufio.NewReader(pluginToHostR),
		done:       make(chan error, 1),
	}
	go func() { h.done <- h.server.Serve() }()
	t.Cleanup(func() {
		_ = hostToPluginW.Close()
		_ = pluginToHostR.Close()
		select {
		case <-h.done:
		case <-time.After(time.Second):
			t.Error("Serve did not stop")
		}
	})
	return h
}

func (h *peerHarness) read(t *testing.T) map[string]any {
	t.Helper()
	line, err := h.fromServer.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(line, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func (h *peerHarness) write(t *testing.T, value any) {
	t.Helper()
	if err := json.NewEncoder(h.toServer).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func TestCallCorrelatesConcurrentResponses(t *testing.T) {
	h := newPeerHarness(t)
	type result struct {
		Value string `json:"value"`
	}
	results := make([]result, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = h.server.Call(context.Background(), 44, "host.history.query", map[string]any{"slot": i}, &results[i])
		}(i)
	}
	first := h.read(t)
	second := h.read(t)
	firstID := int64(first["id"].(float64))
	secondID := int64(second["id"].(float64))
	h.write(t, map[string]any{"jsonrpc": "2.0", "id": secondID, "result": map[string]any{"value": "second"}})
	h.write(t, map[string]any{"jsonrpc": "2.0", "id": firstID, "result": map[string]any{"value": "first"}})
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("call errors: %v", errs)
	}
	values := map[string]bool{results[0].Value: true, results[1].Value: true}
	if !values["first"] || !values["second"] {
		t.Fatalf("unexpected results: %+v", results)
	}
	for _, request := range []map[string]any{first, second} {
		params := request["params"].(map[string]any)
		if params["parent_request_id"] != float64(44) {
			t.Fatalf("missing parent request: %+v", request)
		}
	}
}

func TestCancellationReachesHandler(t *testing.T) {
	h := newPeerHarness(t)
	canceled := make(chan struct{})
	h.server.Handle("command.finished", func(request Request) (any, *Error) {
		<-request.Context.Done()
		close(canceled)
		return map[string]any{"corrections": []string{}}, nil
	})
	h.write(t, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "command.finished", "params": map[string]any{}})
	h.write(t, map[string]any{"jsonrpc": "2.0", "method": "$/cancelRequest", "params": map[string]any{"id": 9}})
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("handler context was not canceled")
	}
	response := h.read(t)
	if response["id"] != float64(9) {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestCanceledCallRemovesPendingRequest(t *testing.T) {
	h := newPeerHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- h.server.Call(ctx, 7, "host.completion.query", map[string]any{"line": "gti"}, nil)
	}()
	request := h.read(t)
	cancel()
	cancelMessage := h.read(t)
	if cancelMessage["method"] != "$/cancelRequest" {
		t.Fatalf("unexpected cancellation: %+v", cancelMessage)
	}
	if err := <-done; err != context.Canceled {
		t.Fatalf("got %v, want context canceled", err)
	}
	h.server.mu.Lock()
	pending := len(h.server.pending)
	h.server.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending request leaked after cancel: %+v", request)
	}
}

func TestNotificationInvokesHandlerWithoutResponse(t *testing.T) {
	h := newPeerHarness(t)
	called := make(chan struct{})
	h.server.Handle("session.start", func(request Request) (any, *Error) {
		if request.ID != 0 {
			t.Errorf("notification id=%d", request.ID)
		}
		close(called)
		return nil, nil
	})
	h.write(t, map[string]any{"jsonrpc": "2.0", "method": "session.start", "params": map[string]any{"cwd": "/work"}})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("notification handler was not called")
	}
}

func TestMalformedFrameStopsPeer(t *testing.T) {
	server := New(strings.NewReader("not-json\n"), io.Discard)
	if err := server.Serve(); err == nil || !strings.Contains(err.Error(), "invalid JSON-RPC") {
		t.Fatalf("Serve() error = %v, want invalid JSON-RPC", err)
	}
}

func TestHandlerErrorIsEncoded(t *testing.T) {
	h := newPeerHarness(t)
	h.server.Handle("command.finished", func(Request) (any, *Error) {
		return nil, &Error{Code: -32602, Message: "invalid params"}
	})
	h.write(t, map[string]any{"jsonrpc": "2.0", "id": 17, "method": "command.finished", "params": map[string]any{}})
	response := h.read(t)
	rpcErr, ok := response["error"].(map[string]any)
	if !ok || rpcErr["code"] != float64(-32602) || rpcErr["message"] != "invalid params" {
		t.Fatalf("unexpected handler error response: %+v", response)
	}
}

func TestPeerExitUnblocksPendingCall(t *testing.T) {
	h := newPeerHarness(t)
	done := make(chan error, 1)
	go func() {
		done <- h.server.Call(context.Background(), 5, "host.history.query", map[string]any{"limit": 1}, nil)
	}()
	_ = h.read(t)
	if err := h.toServer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("Call() error = %v, want peer-closed error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending call was not released after peer exit")
	}
}
