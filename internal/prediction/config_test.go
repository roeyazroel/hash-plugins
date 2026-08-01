package prediction

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseConfigDefaultsAndOverrides(t *testing.T) {
	cfg, err := ParseConfig(json.RawMessage(`{"confidence_threshold":0.75,"learn_from_other_shells":true,"shells":["zsh"],"history_paths":{"zsh":"~/.zsh_history"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfidenceThreshold != 0.75 || !cfg.LearnFromOtherShells || len(cfg.Shells) != 1 || cfg.HistoryPaths["zsh"] != "~/.zsh_history" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestParseConfigRejectsUnsafeSettings(t *testing.T) {
	for _, raw := range []string{
		`{"confidence_threshold":0}`,
		`{"confidence_threshold":1.1}`,
		`{"shells":["zsh","zsh"]}`,
		`{"shells":["powershell"]}`,
		`{"shells":["zsh"],"history_paths":{"bash":"/tmp/bash"}}`,
	} {
		if _, err := ParseConfig(json.RawMessage(raw)); err == nil {
			t.Errorf("ParseConfig(%s) succeeded", raw)
		}
	}
}

func TestParseConfigErrorDoesNotEchoRawSettings(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{"history_paths":{"zsh":"/private/secret-history"},"shells":["zsh","zsh"]}`))
	if err == nil || strings.Contains(err.Error(), "secret-history") {
		t.Fatalf("error = %v", err)
	}
}
