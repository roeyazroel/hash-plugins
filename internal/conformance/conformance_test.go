package conformance

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type peer struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
}

func startPeer(t *testing.T, root string, command string, args ...string) *peer {
	t.Helper()
	cmd := exec.Command(command, args...) //nolint:gosec // fixed local conformance commands
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &peer{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout)}
	p.scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	return p
}

func (p *peer) write(t *testing.T, message any) {
	t.Helper()
	if err := json.NewEncoder(p.stdin).Encode(message); err != nil {
		t.Fatal(err)
	}
}

func (p *peer) read(t *testing.T) map[string]any {
	t.Helper()
	if !p.scanner.Scan() {
		t.Fatalf("peer closed: %v", p.scanner.Err())
	}
	var message map[string]any
	if err := json.Unmarshal(p.scanner.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func initialize(t *testing.T, p *peer) {
	t.Helper()
	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocol_version": 1, "hash_version": "test", "plugin": map[string]any{"id": "io.runhash.test", "version": "0.1.0"}, "hooks": []string{"command.finished"}, "settings": map[string]any{}, "cwd": "/work", "dialect": "bash"}})
	response := p.read(t)
	if response["id"] != float64(1) || response["error"] != nil {
		t.Fatalf("initialize response: %+v", response)
	}
}

func runCorrectionExchange(t *testing.T, p *peer) {
	t.Helper()
	initialize(t, p)
	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "command.finished", "params": map[string]any{"executed_line": "git sttaus", "exit_code": 1, "failure_kind": "exit_status", "stderr_tail": "git: 'sttaus' is not a git command", "cwd": "/work"}})
	for calls := 0; calls < 2; calls++ {
		request := p.read(t)
		method, _ := request["method"].(string)
		id := request["id"]
		switch method {
		case "host.history.query":
			p.write(t, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"entries": []any{map[string]any{"line": "git status", "cwd": "/work", "exit_code": 0}}}})
		case "host.completion.query":
			p.write(t, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"items": []any{map[string]any{"label": "status", "insert_text": "status"}}}})
		default:
			t.Fatalf("unexpected nested request: %+v", request)
		}
	}
	response := p.read(t)
	result, _ := response["result"].(map[string]any)
	corrections, _ := result["corrections"].([]any)
	if len(corrections) != 1 || corrections[0] != "git status" {
		t.Fatalf("correction response: %+v", response)
	}
	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "shutdown", "params": map[string]any{}})
	if response := p.read(t); response["id"] != float64(8) {
		t.Fatalf("shutdown response: %+v", response)
	}
}

func TestAutocorrectionPluginConformance(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runCorrectionExchange(t, startPeer(t, root, "go", "run", "./plugins/autocorrection"))
}

func TestGoAllHooksExampleConformance(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runCorrectionExchange(t, startPeer(t, root, "go", "run", "./examples/go-all-hooks"))
}

func TestPythonAllHooksExampleConformance(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runCorrectionExchange(t, startPeer(t, root, "python3", "examples/python-all-hooks/all_hooks.py"))
}
