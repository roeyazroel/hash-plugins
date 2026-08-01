package correction

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	commandHelpTimeout   = 140 * time.Millisecond
	maxCommandHelpBytes  = 64 * 1024
	maxCommandVocabulary = 512
	commandHelpWaitDelay = 20 * time.Millisecond
)

var prescribedHelpPattern = regexp.MustCompile(`(?im)(?:run|try)[\t ]+['"]?([[:alnum:]_.-]+)[\t ]+(--help|-h)['"]?(?:[\t ]|$)`)

// DiscoverCommandVocabulary follows only a diagnostic-prescribed help command
// for the same executable Hash just ran. It never invokes a shell, substitutes
// arguments, or follows a hint naming a different program.
func DiscoverCommandVocabulary(ctx context.Context, command, diagnostic string) ([]string, error) {
	return discoverCommandVocabulary(ctx, command, diagnostic, commandHelpTimeout)
}

func discoverCommandVocabulary(ctx context.Context, command, diagnostic string, timeout time.Duration) ([]string, error) {
	helpFlag, ok := prescribedHelpFlag(command, diagnostic)
	if !ok {
		return nil, nil
	}
	commandPath, err := exec.LookPath(command)
	if err != nil {
		return nil, fmt.Errorf("resolve command help executable: %w", err)
	}
	helpCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout := boundedBuffer{limit: maxCommandHelpBytes / 2}
	stderr := boundedBuffer{limit: maxCommandHelpBytes / 2}
	cmd := exec.CommandContext(helpCtx, commandPath, helpFlag) //nolint:gosec // exact previously-run executable plus a validated help flag
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = commandHelpWaitDelay
	runErr := cmd.Run()
	if helpCtx.Err() != nil {
		return nil, helpCtx.Err()
	}
	vocabulary := parseCommandVocabulary(stdout.String() + "\n" + stderr.String())
	if len(vocabulary) > 0 {
		return vocabulary, nil
	}
	if runErr != nil {
		return nil, fmt.Errorf("query command help: %w", runErr)
	}
	return nil, nil
}

func prescribedHelpFlag(command, diagnostic string) (string, bool) {
	for _, match := range prescribedHelpPattern.FindAllStringSubmatch(diagnostic, 2) {
		if len(match) == 3 && match[1] == filepath.Base(command) {
			return match[2], true
		}
	}
	return "", false
}

func parseCommandVocabulary(output string) []string {
	seen := make(map[string]bool)
	vocabulary := make([]string, 0, 32)
	inCommands := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if commandSectionHeading(trimmed) {
			inCommands = true
			continue
		}
		if !inCommands {
			continue
		}
		if trimmed == "" || len(line) == len(strings.TrimLeft(line, " \t")) {
			inCommands = false
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		candidate := strings.TrimSuffix(fields[0], "*")
		if seen[candidate] || !validDiagnosticCandidate(candidate) {
			continue
		}
		seen[candidate] = true
		vocabulary = append(vocabulary, candidate)
		if len(vocabulary) >= maxCommandVocabulary {
			break
		}
	}
	return vocabulary
}

func commandSectionHeading(line string) bool {
	lower := strings.ToLower(line)
	return strings.HasSuffix(lower, "commands:") && len(strings.Fields(line)) <= 3
}

type boundedBuffer struct {
	mu    sync.Mutex
	data  bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	originalLength := len(p)
	remaining := b.limit - b.data.Len()
	if remaining > 0 {
		_, _ = b.data.Write(p[:min(len(p), remaining)])
	}
	return originalLength, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}
