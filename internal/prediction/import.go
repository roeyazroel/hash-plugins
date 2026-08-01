package prediction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxHistoryBytes = 32 * 1024 * 1024
const maxHistoryCommands = 50000

func defaultHistoryPaths(shells []string) map[string]string {
	home, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".local", "share")
	}
	paths := map[string]string{"bash": filepath.Join(home, ".bash_history"), "zsh": filepath.Join(home, ".zsh_history"), "fish": filepath.Join(xdg, "fish", "fish_history")}
	out := map[string]string{}
	for _, s := range shells {
		out[s] = paths[s]
	}
	return out
}
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func importHistories(paths map[string]string, shells []string) ([][2]string, error) {
	var pairs [][2]string
	for _, shell := range shells {
		path := expandPath(paths[shell])
		if path == "" {
			continue
		}
		entries, err := readHistory(path, shell)
		if err != nil {
			continue
		}
		var prev string
		for _, line := range entries {
			if safeCommand(line) != nil {
				prev = ""
				continue
			}
			if prev != "" {
				pairs = append(pairs, [2]string{prev, line})
			}
			prev = line
		}
	}
	return pairs, nil
}
func readHistory(path, shell string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxHistoryBytes {
		data = data[len(data)-maxHistoryBytes:]
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxHistoryCommands {
		lines = lines[len(lines)-maxHistoryCommands:]
	}
	out := []string{}
	for _, line := range lines {
		var cmd string
		switch shell {
		case "zsh":
			cmd = parseZsh(line)
		case "fish":
			cmd = parseFish(line)
		default:
			cmd = parseBash(line)
		}
		if cmd != "" {
			out = append(out, cmd)
		}
	}
	return out, nil
}
func parseBash(line string) string {
	if strings.HasPrefix(line, "#") {
		if _, err := strconv.ParseInt(strings.TrimPrefix(line, "#"), 10, 64); err == nil {
			return ""
		}
	}
	return strings.TrimSpace(line)
}
func parseZsh(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, ": ") {
		if i := strings.Index(line, ";"); i >= 0 {
			return strings.TrimSpace(line[i+1:])
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
		var s string
		if json.Unmarshal([]byte(raw), &s) == nil {
			return s
		}
	}
	return strings.Trim(raw, "'")
}
