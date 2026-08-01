package autosuggestion

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseConfigDefaultsAndOverrides(t *testing.T) {
	defaults := DefaultConfig()
	if defaults.LearnFromOtherShells || defaults.HistoryLimit != 100 {
		t.Fatalf("defaults = %+v", defaults)
	}
	if got, want := strings.Join(defaults.Shells, ","), "zsh,bash,fish"; got != want {
		t.Fatalf("default shells = %q, want %q", got, want)
	}

	cfg, err := ParseConfig(json.RawMessage(`{"learn_from_other_shells":true,"shells":["fish"],"history_paths":{"fish":"~/.local/share/fish/fish_history"},"history_limit":12}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LearnFromOtherShells || cfg.HistoryLimit != 12 || len(cfg.Shells) != 1 || cfg.HistoryPaths["fish"] == "" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestParseConfigRejectsUnknownUnsafeAndOutOfRangeSettings(t *testing.T) {
	for _, raw := range []string{
		`{"history_limit":0}`,
		`{"history_limit":101}`,
		`{"unknown":true}`,
		`{"shells":["zsh","zsh"]}`,
		`{"shells":["powershell"]}`,
		`{"shells":["zsh"],"history_paths":{"bash":"/tmp/history"}}`,
		`{"shells":["zsh"],"history_paths":{"zsh":"../secret"}}`,
		`{"shells":["zsh"],"history_paths":{"zsh":"/"}}`,
	} {
		if _, err := ParseConfig(json.RawMessage(raw)); err == nil {
			t.Errorf("ParseConfig(%s) succeeded", raw)
		}
	}
}

func TestParseConfigErrorsRedactConfiguredPaths(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{"shells":["zsh"],"history_paths":{"zsh":"../private/secret-history"}}`))
	if err == nil || strings.Contains(err.Error(), "secret-history") {
		t.Fatalf("error = %v", err)
	}
}
