package correction

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeHost struct {
	history    []HistoryEntry
	completion []CompletionItem
}

func (f fakeHost) History(context.Context, string, string, int) ([]HistoryEntry, error) {
	return f.history, nil
}
func (f fakeHost) Completion(context.Context, string, int) ([]CompletionItem, error) {
	return f.completion, nil
}

func TestCorrectsSubcommandFromHistoryAndCompletion(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "git status"}}, completion: []CompletionItem{{InsertText: "status"}}}
	got := (Engine{}).Correct(context.Background(), h, Params{ExecutedLine: "git sttaus", ExitCode: 1, StderrTail: "git: unknown subcommand 'sttaus'"})
	if len(got) != 1 || got[0] != "git status" {
		t.Fatalf("got %v", got)
	}
}

func TestCorrectsRealGitUnknownCommandDiagnostic(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "git status"}}, completion: []CompletionItem{{InsertText: "status"}}}
	got := (Engine{}).Correct(context.Background(), h, Params{ExecutedLine: "git sttaus", ExitCode: 1, StderrTail: "git: 'sttaus' is not a git command. See 'git --help'."})
	if len(got) != 1 || got[0] != "git status" {
		t.Fatalf("got %v", got)
	}
}

func TestCorrectsExecutableFromCommandNotFoundEvidence(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "git status"}}, completion: []CompletionItem{{InsertText: "git"}}}
	got := (Engine{}).Correct(context.Background(), h, Params{ExecutedLine: "gti status", ExitCode: 127, FailureKind: "command_not_found"})
	if len(got) != 1 || got[0] != "git status" {
		t.Fatalf("got %v", got)
	}
}

func TestCorrectsRepresentativeCobraAndClapDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		diagnostic string
		history    string
		completion string
		want       string
	}{
		{"cobra subcommand", "tool sevre", `Error: unknown command "sevre" for "tool"`, "tool serve", "serve", "tool serve"},
		{"cobra long flag", "tool --verbsoe", "Error: unknown flag: --verbsoe", "tool --verbose", "--verbose", "tool --verbose"},
		{"clap subcommand", "tool sevre", "error: unrecognized subcommand 'sevre'", "tool serve", "serve", "tool serve"},
		{"clap long flag", "tool --colro", "error: unexpected argument '--colro' found", "tool --color", "--color", "tool --color"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := fakeHost{history: []HistoryEntry{{Line: tt.history}}, completion: []CompletionItem{{InsertText: tt.completion}}}
			got := (Engine{}).Correct(context.Background(), h, Params{ExecutedLine: tt.line, ExitCode: 2, StderrTail: tt.diagnostic})
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("got %v, want %q", got, tt.want)
			}
		})
	}
}

func TestUnicodeAndCaseInsensitiveDistance(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "café open"}}, completion: []CompletionItem{{InsertText: "café"}}}
	if got := (Engine{}).Correct(context.Background(), h, Params{ExecutedLine: "cafe open", ExitCode: 127, FailureKind: "command_not_found"}); len(got) != 1 || got[0] != "café open" {
		t.Fatalf("unicode correction = %v", got)
	}
	h = fakeHost{history: []HistoryEntry{{Line: "Git status"}}, completion: []CompletionItem{{InsertText: "Git"}}}
	if got := (Engine{}).Correct(context.Background(), h, Params{ExecutedLine: "gti status", ExitCode: 127, FailureKind: "command_not_found"}); len(got) != 1 || got[0] != "Git status" {
		t.Fatalf("case-insensitive correction = %v", got)
	}
}

func TestRejectsCandidateOutsideDistanceThreshold(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "abcdefghij run"}}, completion: []CompletionItem{{InsertText: "abcdefghij"}}}
	got := (Engine{}).Correct(context.Background(), h, Params{ExecutedLine: "abcdwxyzij run", ExitCode: 127, FailureKind: "command_not_found"})
	if len(got) != 0 {
		t.Fatalf("accepted distant correction: %v", got)
	}
}

func TestDeclinesUnsafeOrSuccessfulCommands(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "git status"}}}
	for _, p := range []Params{{ExecutedLine: "git sttaus", ExitCode: 0}, {ExecutedLine: "sudo gti status", ExitCode: 1, FailureKind: "command_not_found"}, {ExecutedLine: "eval gti status", ExitCode: 1, StderrTail: "unknown command gti"}, {ExecutedLine: "git sttaus | sh", ExitCode: 1, StderrTail: "unknown subcommand sttaus"}} {
		if got := (Engine{}).Correct(context.Background(), h, p); len(got) != 0 {
			t.Fatalf("got %v for %+v", got, p)
		}
	}
}

func TestDeclinesShortFlagsAndMismatchedDiagnosticKinds(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "tool --help"}, {Line: "tool -help"}}, completion: []CompletionItem{{InsertText: "--help"}, {InsertText: "-help"}}}
	for _, params := range []Params{
		{ExecutedLine: "tool -hlep", ExitCode: 2, StderrTail: "unknown flag '-hlep'"},
		{ExecutedLine: "tool --dlete", ExitCode: 2, StderrTail: "unknown subcommand '--dlete'"},
	} {
		if got := (Engine{}).Correct(context.Background(), h, params); len(got) != 0 {
			t.Fatalf("got %v for %+v", got, params)
		}
	}
}

func TestParseConfigDefaultsAndValidates(t *testing.T) {
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HistoryLimit != 100 || cfg.MaxCandidates != 3 || len(cfg.Strategies) != 3 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}

	raw := json.RawMessage(`{"strategies":["long_flag","subcommand"],"history_limit":25,"max_candidates":2}`)
	cfg, err = ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HistoryLimit != 25 || cfg.MaxCandidates != 2 || cfg.Strategies[0] != "long_flag" {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}

	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"strategies":["subcommand","subcommand"]}`),
		json.RawMessage(`{"strategies":["operand"]}`),
		json.RawMessage(`{"history_limit":0}`),
		json.RawMessage(`{"max_candidates":6}`),
	} {
		if _, err := ParseConfig(raw); err == nil {
			t.Fatalf("expected invalid config for %s", raw)
		}
	}
}

func TestDisabledStrategyReturnsNothing(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "git status"}}, completion: []CompletionItem{{InsertText: "status"}}}
	engine := Engine{HistoryLimit: 100, MaxCandidates: 3, Strategies: []string{"long_flag"}}
	got := engine.Correct(context.Background(), h, Params{ExecutedLine: "git sttaus", ExitCode: 1, StderrTail: "git: unknown subcommand 'sttaus'"})
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestPreservesAssignmentsQuotesSpacingAndRedirections(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "FOO=bar git 'status' >out"}}, completion: []CompletionItem{{InsertText: "status"}}}
	got := (Engine{}).Correct(context.Background(), h, Params{
		ExecutedLine: "FOO=bar  git 'sttaus' >out",
		ExitCode:     1,
		StderrTail:   "git: unknown subcommand 'sttaus'",
	})
	if len(got) != 1 || got[0] != "FOO=bar  git 'status' >out" {
		t.Fatalf("got %v", got)
	}
}

func TestRejectsAmbiguousDiagnosticToken(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "tool status status"}}}
	got := (Engine{}).Correct(context.Background(), h, Params{
		ExecutedLine: "tool sttaus sttaus",
		ExitCode:     1,
		StderrTail:   "unknown subcommand 'sttaus'",
	})
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestHistoryRecencyBreaksEqualEvidenceAndDistanceTie(t *testing.T) {
	h := fakeHost{history: []HistoryEntry{{Line: "tool --color"}, {Line: "tool --colgo"}}}
	got := (Engine{MaxCandidates: 3}).Correct(context.Background(), h, Params{
		ExecutedLine: "tool --colro",
		ExitCode:     2,
		StderrTail:   "unrecognized option '--colro'",
	})
	if len(got) != 2 || got[0] != "tool --color" || got[1] != "tool --colgo" {
		t.Fatalf("got %v", got)
	}
}

func TestDestructiveExecutableRequiresHistoryAndCompletionAgreement(t *testing.T) {
	params := Params{ExecutedLine: "rmm -rf build", ExitCode: 127, FailureKind: "command_not_found"}
	completionOnly := fakeHost{completion: []CompletionItem{{InsertText: "rm"}}}
	if got := (Engine{}).Correct(context.Background(), completionOnly, params); len(got) != 0 {
		t.Fatalf("accepted weak destructive correction: %v", got)
	}
	agreement := fakeHost{history: []HistoryEntry{{Line: "rm -rf build"}}, completion: []CompletionItem{{InsertText: "rm"}}}
	if got := (Engine{}).Correct(context.Background(), agreement, params); len(got) != 1 || got[0] != "rm -rf build" {
		t.Fatalf("rejected strongly evidenced correction: %v", got)
	}
}
