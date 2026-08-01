package autosuggestion

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

var supportedShells = map[string]bool{"bash": true, "zsh": true, "fish": true}

type Config struct {
	LearnFromOtherShells bool
	Shells               []string
	HistoryPaths         map[string]string
	HistoryLimit         int
}

func DefaultConfig() Config {
	return Config{
		Shells:       []string{"zsh", "bash", "fish"},
		HistoryPaths: map[string]string{},
		HistoryLimit: 100,
	}
}

func ParseConfig(raw json.RawMessage) (Config, error) {
	cfg := DefaultConfig()
	if len(raw) == 0 || string(raw) == "null" {
		return cfg, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Config{}, errors.New("settings are not valid JSON")
	}
	for field := range fields {
		switch field {
		case "learn_from_other_shells", "shells", "history_paths", "history_limit":
		default:
			return Config{}, errors.New("unknown autosuggestions setting")
		}
	}
	var input struct {
		LearnFromOtherShells *bool              `json:"learn_from_other_shells"`
		Shells               *[]string          `json:"shells"`
		HistoryPaths         *map[string]string `json:"history_paths"`
		HistoryLimit         *int               `json:"history_limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return Config{}, errors.New("settings are not valid JSON")
	}
	if input.LearnFromOtherShells != nil {
		cfg.LearnFromOtherShells = *input.LearnFromOtherShells
	}
	if input.Shells != nil {
		seen := map[string]bool{}
		for _, shell := range *input.Shells {
			if !supportedShells[shell] || seen[shell] {
				return Config{}, errors.New("shell selection contains an unsupported or duplicate shell")
			}
			seen[shell] = true
		}
		cfg.Shells = append([]string(nil), (*input.Shells)...)
	}
	if len(cfg.Shells) == 0 {
		return Config{}, errors.New("at least one shell must be selected")
	}
	if input.HistoryLimit != nil {
		cfg.HistoryLimit = *input.HistoryLimit
	}
	if cfg.HistoryLimit < 1 || cfg.HistoryLimit > 100 {
		return Config{}, errors.New("history limit must be between one and one hundred")
	}
	if input.HistoryPaths != nil {
		selected := map[string]bool{}
		for _, shell := range cfg.Shells {
			selected[shell] = true
		}
		cfg.HistoryPaths = make(map[string]string, len(*input.HistoryPaths))
		for shell, path := range *input.HistoryPaths {
			if !selected[shell] || !validConfiguredPath(path) {
				return Config{}, errors.New("history path is invalid")
			}
			cfg.HistoryPaths[shell] = path
		}
	}
	return cfg, nil
}

func validConfiguredPath(path string) bool {
	if path == "" || strings.ContainsAny(path, "\x00\r\n") || path == "/" {
		return false
	}
	if strings.HasPrefix(path, "~/") {
		path = path[2:]
		return path != "" && !pathHasParent(path)
	}
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !pathHasParent(path)
}

func pathHasParent(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}
