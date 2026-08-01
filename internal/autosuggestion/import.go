package autosuggestion

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const maxHistoryBytes int64 = 32 * 1024 * 1024
const maxHistoryCommands = 50000

func defaultHistoryPaths(shells []string) map[string]string {
	home, _ := absoluteUserHome()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(dataHome) || filepath.Clean(dataHome) != dataHome {
		dataHome = ""
	}
	if dataHome == "" && home != "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	defaults := map[string]string{
		"bash": filepath.Join(home, ".bash_history"),
		"zsh":  filepath.Join(home, ".zsh_history"),
		"fish": filepath.Join(dataHome, "fish", "fish_history"),
	}
	paths := make(map[string]string, len(shells))
	for _, shell := range shells {
		paths[shell] = defaults[shell]
	}
	return paths
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, ok := absoluteUserHome()
		if !ok {
			return "", os.ErrInvalid
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", os.ErrInvalid
	}
	return path, nil
}

func absoluteUserHome() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", false
	}
	return home, true
}

type historyReader func(context.Context, string, string, int) ([]string, error)

func readHistory(ctx context.Context, path, shell string, limit int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolved, err := expandPath(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(resolved, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	start := info.Size() - maxHistoryBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := readBounded(ctx, file, maxHistoryBytes)
	if err != nil {
		return nil, err
	}
	if start > 0 {
		if newline := strings.IndexByte(string(data), '\n'); newline >= 0 {
			data = data[newline+1:]
		} else {
			data = nil
		}
	}
	lines := strings.Split(string(data), "\n")
	commands := make([]string, 0, len(lines))
	for _, line := range lines {
		var command string
		switch shell {
		case "zsh":
			command = parseZsh(line)
		case "fish":
			command = parseFish(line)
		default:
			command = parseBash(line)
		}
		if command != "" {
			commands = append(commands, command)
		}
	}
	if len(commands) > maxHistoryCommands {
		commands = commands[len(commands)-maxHistoryCommands:]
	}
	if limit < 1 || limit > len(commands) {
		limit = len(commands)
	}
	newest := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for i := len(commands) - 1; i >= 0 && len(newest) < limit; i-- {
		if _, duplicate := seen[commands[i]]; duplicate {
			continue
		}
		seen[commands[i]] = struct{}{}
		newest = append(newest, commands[i])
	}
	return newest, nil
}

func readBounded(ctx context.Context, reader io.Reader, limit int64) ([]byte, error) {
	data := make([]byte, 0, min(int64(64*1024), limit))
	buffer := make([]byte, 64*1024)
	for int64(len(data)) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := limit - int64(len(data))
		chunk := buffer
		if int64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		count, err := reader.Read(chunk)
		if count > 0 {
			data = append(data, chunk[:count]...)
		}
		if err == io.EOF {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func parseBash(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#") {
		if _, err := strconv.ParseInt(strings.TrimPrefix(line, "#"), 10, 64); err == nil {
			return ""
		}
	}
	return line
}

func parseZsh(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, ": ") {
		if separator := strings.IndexByte(line, ';'); separator >= 0 {
			return strings.TrimSpace(line[separator+1:])
		}
	}
	return line
}

func parseFish(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "- cmd:") {
		return ""
	}
	raw := strings.TrimSpace(strings.TrimPrefix(line, "- cmd:"))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "\"") {
		var decoded string
		if json.Unmarshal([]byte(raw), &decoded) == nil {
			return decoded
		}
		return ""
	}
	if strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") && len(raw) >= 2 {
		return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'")
	}
	decoded, err := strconv.Unquote("\"" + strings.ReplaceAll(raw, "\"", "\\\"") + "\"")
	if err == nil {
		return decoded
	}
	return raw
}
