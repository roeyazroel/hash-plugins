// Package sdk implements the duplex Hash plugin protocol v1 peer.
package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type Request struct {
	JSONRPC      string          `json:"jsonrpc"`
	ID           int64           `json:"id"`
	Method       string          `json:"method"`
	Params       json.RawMessage `json:"params"`
	Context      context.Context `json:"-"`
	Notification bool            `json:"-"`
}
type Handler func(Request) (any, *Error)
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type response struct {
	result json.RawMessage
	err    *Error
}

type queuedWrite struct {
	ctx  context.Context
	data []byte
	done chan error
}

type Server struct {
	in       io.Reader
	out      io.Writer
	handlers map[string]Handler
	mu       sync.Mutex
	pending  map[int64]chan response
	active   map[int64]context.CancelFunc
	nextID   atomic.Int64
	closed   chan struct{}
	writes   chan queuedWrite
}

func New(in io.Reader, out io.Writer) *Server {
	s := &Server{in: in, out: out, handlers: map[string]Handler{}, pending: map[int64]chan response{}, active: map[int64]context.CancelFunc{}, closed: make(chan struct{}), writes: make(chan queuedWrite, 64)}
	go s.writeLoop()
	return s
}
func (s *Server) Handle(method string, handler Handler) { s.handlers[method] = handler }

// Call performs a plugin-originated host request correlated to parentID.
func (s *Server) Call(ctx context.Context, parentID int64, method string, params any, result any) error {
	id := s.nextID.Add(1) + 1000
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	values := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
	}
	values["parent_request_id"] = parentID
	ch := make(chan response, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()
	if err := s.write(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": values}); err != nil {
		return err
	}
	select {
	case reply := <-ch:
		if reply.err != nil {
			return fmt.Errorf("host %s: %s", method, reply.err.Message)
		}
		if result != nil {
			return json.Unmarshal(reply.result, result)
		}
		return nil
	case <-ctx.Done():
		cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_ = s.enqueueNotification(cancelCtx, map[string]any{"jsonrpc": "2.0", "method": "$/cancelRequest", "params": map[string]any{"id": id}})
		cancel()
		return ctx.Err()
	case <-s.closed:
		return errors.New("Hash connection closed")
	}
}

func (s *Server) Serve() error {
	defer close(s.closed)
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int64          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			Result  json.RawMessage `json:"result"`
			Error   *Error          `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil || message.JSONRPC != "2.0" {
			return errors.New("invalid JSON-RPC message")
		}
		if message.Method == "$/cancelRequest" {
			var p struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(message.Params, &p)
			s.mu.Lock()
			cancel := s.active[p.ID]
			s.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			continue
		}
		if message.Method != "" {
			if message.ID == nil {
				go s.handle(Request{JSONRPC: "2.0", Method: message.Method, Params: message.Params, Context: context.Background(), Notification: true})
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			s.mu.Lock()
			s.active[*message.ID] = cancel
			s.mu.Unlock()
			go s.handle(Request{JSONRPC: "2.0", ID: *message.ID, Method: message.Method, Params: message.Params, Context: ctx})
			continue
		}
		if message.ID == nil {
			return errors.New("response id is required")
		}
		s.mu.Lock()
		ch := s.pending[*message.ID]
		delete(s.pending, *message.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- response{result: message.Result, err: message.Error}
		}
	}
	return scanner.Err()
}

func (s *Server) handle(req Request) {
	if req.Notification {
		if h := s.handlers[req.Method]; h != nil {
			_, _ = h(req)
		}
		return
	}
	defer func() {
		s.mu.Lock()
		if cancel := s.active[req.ID]; cancel != nil {
			cancel()
		}
		delete(s.active, req.ID)
		s.mu.Unlock()
	}()
	h := s.handlers[req.Method]
	if h == nil {
		s.reply(req.ID, nil, &Error{-32601, "method not found"})
		return
	}
	value, rpcErr := h(req)
	s.reply(req.ID, value, rpcErr)
}
func (s *Server) reply(id int64, value any, rpcErr *Error) {
	message := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		message["error"] = rpcErr
	} else {
		message["result"] = value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = s.write(ctx, message)
}
func (s *Server) write(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	job := queuedWrite{ctx: ctx, data: data, done: make(chan error, 1)}
	select {
	case s.writes <- job:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.closed:
		return errors.New("Hash connection closed")
	}
	select {
	case err := <-job.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.closed:
		return errors.New("Hash connection closed")
	}
}

// enqueueNotification queues a best-effort protocol notification without
// waiting for the peer to consume it. The queued job deliberately owns a
// background context: the caller's short enqueue deadline must not cause the
// writer to discard a notification after it has accepted it.
func (s *Server) enqueueNotification(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	job := queuedWrite{ctx: context.Background(), data: data, done: make(chan error, 1)}
	select {
	case s.writes <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.closed:
		return errors.New("Hash connection closed")
	}
}

func (s *Server) writeLoop() {
	for {
		select {
		case job := <-s.writes:
			if err := job.ctx.Err(); err != nil {
				job.done <- err
				continue
			}
			_, err := s.out.Write(job.data)
			job.done <- err
		case <-s.closed:
			return
		}
	}
}
