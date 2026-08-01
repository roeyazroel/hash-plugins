package prediction

import (
	"encoding/json"
	"errors"
	"fmt"
)

var supportedShells = map[string]bool{"bash": true, "zsh": true, "fish": true}

type Config struct {
	ConfidenceThreshold  float64
	LearnFromOtherShells bool
	Shells               []string
	HistoryPaths         map[string]string
}

func DefaultConfig() Config {
	return Config{ConfidenceThreshold: 0.6, Shells: []string{"zsh", "bash", "fish"}, HistoryPaths: map[string]string{}}
}

func ParseConfig(raw json.RawMessage) (Config, error) {
	cfg := DefaultConfig()
	if len(raw) == 0 || string(raw) == "null" {
		return cfg, nil
	}
	var in struct {
		ConfidenceThreshold  *float64          `json:"confidence_threshold"`
		LearnFromOtherShells *bool             `json:"learn_from_other_shells"`
		Shells               []string          `json:"shells"`
		HistoryPaths         map[string]string `json:"history_paths"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return Config{}, errors.New("settings are not valid JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Config{}, errors.New("settings are not valid JSON")
	}
	for name := range fields {
		switch name {
		case "confidence_threshold", "learn_from_other_shells", "shells", "history_paths":
		default:
			return Config{}, fmt.Errorf("unknown prediction setting")
		}
	}
	if in.ConfidenceThreshold != nil {
		cfg.ConfidenceThreshold = *in.ConfidenceThreshold
	}
	if cfg.ConfidenceThreshold <= 0 || cfg.ConfidenceThreshold > 1 {
		return Config{}, errors.New("confidence threshold must be greater than zero and at most one")
	}
	if in.LearnFromOtherShells != nil {
		cfg.LearnFromOtherShells = *in.LearnFromOtherShells
	}
	if in.Shells != nil {
		seen := map[string]bool{}
		for _, shell := range in.Shells {
			if !supportedShells[shell] || seen[shell] {
				return Config{}, errors.New("shell selection contains an unsupported or duplicate shell")
			}
			seen[shell] = true
		}
		cfg.Shells = append([]string(nil), in.Shells...)
	}
	for shell, path := range in.HistoryPaths {
		selected := false
		for _, s := range cfg.Shells {
			if s == shell {
				selected = true
				break
			}
		}
		if !selected || path == "" {
			return Config{}, errors.New("history path must name a selected shell and be non-empty")
		}
	}
	if in.HistoryPaths != nil {
		cfg.HistoryPaths = map[string]string{}
		for k, v := range in.HistoryPaths {
			cfg.HistoryPaths[k] = v
		}
	}
	return cfg, nil
}
