package conformance

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
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
	return startPeerEnv(t, root, nil, command, args...)
}

func startPeerEnv(t *testing.T, root string, env []string, command string, args ...string) *peer {
	t.Helper()
	cmd := exec.Command(command, args...) //nolint:gosec // fixed local conformance commands
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
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

func TestAutosuggestionsPluginConformance(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dataHome := filepath.Join(t.TempDir(), "data")
	p := startPeerEnv(t, root, []string{"XDG_DATA_HOME=" + dataHome}, "go", "run", "./plugins/autosuggestions")

	// Doctor validates settings and handshake without touching plugin data.
	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocol_version": 1, "session_kind": "doctor",
		"settings": map[string]any{"learn_from_other_shells": true, "shells": []string{"bash"}, "history_paths": map[string]string{"bash": "/missing/history"}},
	}})
	if response := p.read(t); response["id"] != float64(1) || response["error"] != nil {
		t.Fatalf("doctor initialize response: %+v", response)
	}
	if _, err := os.Stat(filepath.Join(dataHome, "hash")); !os.IsNotExist(err) {
		t.Fatalf("doctor created plugin data: %v", err)
	}

	// Reinitialize interactively with external learning disabled.
	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "initialize", "params": map[string]any{"protocol_version": 1, "session_kind": "interactive", "settings": map[string]any{}}})
	if response := p.read(t); response["id"] != float64(2) || response["error"] != nil {
		t.Fatalf("interactive initialize response: %+v", response)
	}
	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "editor.suggest", "params": map[string]any{"generation": 1, "trigger": "prompt", "line": "", "cursor": 0, "cwd": "/work", "dialect": "bash"}})
	if response := p.read(t); suggestionText(response) != "" {
		t.Fatalf("prompt suggestion response: %+v", response)
	}

	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "editor.suggest", "params": map[string]any{"generation": 1, "trigger": "edit", "line": "git", "cursor": 3, "cwd": "/work", "dialect": "bash", "previous": map[string]any{"line": "false", "exit_code": 1}}})
	seenCWD, seenGlobal := false, false
	for calls := 0; calls < 2; calls++ {
		request := p.read(t)
		if request["method"] != "host.history.query" {
			t.Fatalf("nested history request: %+v", request)
		}
		params, _ := request["params"].(map[string]any)
		if params["parent_request_id"] != float64(4) || params["prefix"] != "git" || params["limit"] != float64(100) {
			t.Fatalf("nested history params: %+v", request)
		}
		cwd, _ := params["cwd"].(string)
		entries := []any{map[string]any{"line": "git global", "cwd": "/other", "exit_code": 0}}
		if cwd == "/work" {
			seenCWD = true
			entries = []any{map[string]any{"line": "git local", "cwd": "/work", "exit_code": 0}}
		} else {
			seenGlobal = true
		}
		p.write(t, map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"entries": entries}})
	}
	if !seenCWD || !seenGlobal {
		t.Fatalf("history queries cwd=%v global=%v", seenCWD, seenGlobal)
	}
	if response := p.read(t); suggestionText(response) != "git local" {
		t.Fatalf("edit suggestion response: %+v", response)
	}

	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "editor.suggest", "params": map[string]any{"generation": 1, "trigger": "edit", "line": "gi", "cursor": 2, "cwd": "/work", "dialect": "bash"}})
	firstNested := p.read(t)
	if firstNested["method"] != "host.history.query" {
		t.Fatalf("nested cancellation request: %+v", firstNested)
	}
	p.write(t, map[string]any{"jsonrpc": "2.0", "method": "$/cancelRequest", "params": map[string]any{"id": 5}})
	for {
		message := p.read(t)
		if message["method"] == "host.history.query" {
			continue
		}
		if message["method"] == "$/cancelRequest" {
			continue
		}
		if message["id"] == float64(5) {
			rpcErr, _ := message["error"].(map[string]any)
			if rpcErr["code"] != float64(-32800) {
				t.Fatalf("cancellation response: %+v", message)
			}
			break
		}
	}

	p.write(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "shutdown", "params": map[string]any{}})
	for {
		response := p.read(t)
		if response["method"] == "$/cancelRequest" || response["method"] == "host.history.query" {
			continue
		}
		if response["id"] != float64(6) || response["error"] != nil {
			t.Fatalf("shutdown response: %+v", response)
		}
		break
	}
}

func suggestionText(response map[string]any) string {
	result, _ := response["result"].(map[string]any)
	text, _ := result["text"].(string)
	return text
}
