package correction

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

type fakeHost struct {
	history    []HistoryEntry
	completion []CompletionItem
}

type vocabularyHost struct {
	fakeHost
	vocabulary     []string
	seenCommand    string
	seenDiagnostic string
}

func (h *vocabularyHost) CommandVocabulary(_ context.Context, command, diagnostic string) ([]string, error) {
	h.seenCommand, h.seenDiagnostic = command, diagnostic
	return h.vocabulary, nil
}

type delayedVocabularyHost struct{ delay time.Duration }

func (h delayedVocabularyHost) History(ctx context.Context, _, _ string, _ int) ([]HistoryEntry, error) {
	if !waitForEvidence(ctx, h.delay) {
		return nil, ctx.Err()
	}
	return nil, nil
}

func (h delayedVocabularyHost) Completion(ctx context.Context, _ string, _ int) ([]CompletionItem, error) {
	if !waitForEvidence(ctx, h.delay) {
		return nil, ctx.Err()
	}
	return nil, nil
}

func (h delayedVocabularyHost) CommandVocabulary(ctx context.Context, _, _ string) ([]string, error) {
	if !waitForEvidence(ctx, h.delay) {
		return nil, ctx.Err()
	}
	return []string{"ps"}, nil
}

func waitForEvidence(ctx context.Context, delay time.Duration) bool {
	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}

func (f fakeHost) History(context.Context, string, string, int) ([]HistoryEntry, error) {
	return f.history, nil
}
func (f fakeHost) Completion(context.Context, string, int) ([]CompletionItem, error) {
	return f.completion, nil
}

type recordingHost struct {
	historyCWD       string
	completionLine   string
	completionCursor int
	history          []HistoryEntry
	completion       []CompletionItem
}

func (h *recordingHost) History(_ context.Context, _, cwd string, _ int) ([]HistoryEntry, error) {
	h.historyCWD = cwd
	return h.history, nil
}

func (h *recordingHost) Completion(_ context.Context, line string, cursor int) ([]CompletionItem, error) {
	h.completionLine = line
	h.completionCursor = cursor
	return h.completion, nil
}

func TestExecutableCorrectionUsesFailedTokenAndGlobalEvidence(t *testing.T) {
	h := &recordingHost{
		history:    []HistoryEntry{{Line: "git status"}},
		completion: []CompletionItem{{InsertText: "git"}},
	}
	got := (Engine{}).Correct(context.Background(), h, Params{
		ExecutedLine: "got pull",
		ExitCode:     127,
		FailureKind:  "command_not_found",
		CWD:          "/work/project",
	})
	if len(got) != 1 || got[0] != "git pull" {
		t.Fatalf("got %v, want git pull", got)
	}
	if h.completionLine != "got pull" || h.completionCursor != 3 {
		t.Fatalf("completion query = (%q, %d), want (%q, 3)", h.completionLine, h.completionCursor, "got pull")
	}
	if h.historyCWD != "" {
		t.Fatalf("executable history cwd = %q, want global history", h.historyCWD)
	}
}

func TestExecutableCorrectionUsesPluginPATHVocabulary(t *testing.T) {
	got := (Engine{Executables: []string{"jot", "gpt", "git"}}).Correct(context.Background(), fakeHost{}, Params{
		ExecutedLine: "got pull",
		ExitCode:     127,
		FailureKind:  "command_not_found",
	})
	want := []string{"git pull", "gpt pull", "jot pull"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
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

func TestCorrectsQualifiedUnknownCommandDiagnostic(t *testing.T) {
	h := fakeHost{completion: []CompletionItem{{InsertText: "ps"}}}
	got := (Engine{}).Correct(context.Background(), h, Params{
		ExecutedLine: "docker pe",
		ExitCode:     1,
		StderrTail:   "docker: unknown command: docker pe\n\nRun 'docker --help' for more information",
	})
	if len(got) != 1 || got[0] != "docker ps" {
		t.Fatalf("got %v, want docker ps", got)
	}
}

func TestCorrectsQualifiedUnknownCommandFromCommandVocabulary(t *testing.T) {
	diagnostic := "docker: unknown command: docker pe\n\nRun 'docker --help' for more information"
	h := &vocabularyHost{vocabulary: []string{"run", "ps", "pull"}}
	got := (Engine{}).Correct(context.Background(), h, Params{
		ExecutedLine: "docker pe",
		ExitCode:     1,
		StderrTail:   diagnostic,
	})
	if len(got) != 1 || got[0] != "docker ps" {
		t.Fatalf("got %v, want docker ps", got)
	}
	if h.seenCommand != "docker" || h.seenDiagnostic != "\n"+diagnostic {
		t.Fatalf("vocabulary query = (%q, %q)", h.seenCommand, h.seenDiagnostic)
	}
}

func TestCorrectionEvidenceSourcesRunConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	got := (Engine{}).Correct(ctx, delayedVocabularyHost{delay: 60 * time.Millisecond}, Params{
		ExecutedLine: "docker pe",
		ExitCode:     1,
		StderrTail:   "docker: unknown command: docker pe\nRun 'docker --help'",
	})
	if len(got) != 1 || got[0] != "docker ps" {
		t.Fatalf("got %v after %v, want docker ps", got, time.Since(started))
	}
	if elapsed := time.Since(started); elapsed > 120*time.Millisecond {
		t.Fatalf("evidence collection was sequential: %v", elapsed)
	}
}

func TestCorrectsFromGenericDiagnosticAlternativeWithoutOtherEvidence(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		diagnostic string
		want       string
	}{
		{
			name:       "most similar command block",
			line:       "vcs pill",
			diagnostic: "vcs: 'pill' is not a vcs command\n\nThe most similar command is\n\tpull",
			want:       "vcs pull",
		},
		{
			name:       "similar subcommand inline",
			line:       "tool sevre",
			diagnostic: "error: unrecognized subcommand 'sevre'\n\n  tip: a similar subcommand exists: 'serve'",
			want:       "tool serve",
		},
		{
			name:       "did you mean long flag",
			line:       "tool --verbsoe",
			diagnostic: "error: unknown flag '--verbsoe'\nDid you mean '--verbose'?",
			want:       "tool --verbose",
		},
		{
			name:       "full command alternative",
			line:       "pkg isntall",
			diagnostic: "unknown command 'isntall'\nDid you mean this?\n    pkg install",
			want:       "pkg install",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (Engine{}).Correct(context.Background(), fakeHost{}, Params{
				ExecutedLine: tt.line,
				ExitCode:     1,
				StderrTail:   tt.diagnostic,
			})
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("got %v, want %q", got, tt.want)
			}
		})
	}
}

func TestDiagnosticAlternativeMustBeSafeAndReferToFailedToken(t *testing.T) {
	tests := []Params{
		{
			ExecutedLine: "tool delte",
			ExitCode:     1,
			StderrTail:   "unknown subcommand 'delte'\nDid you mean 'destroy'?",
		},
		{
			ExecutedLine: "tool sevre --prod",
			ExitCode:     1,
			StderrTail:   "unknown subcommand 'sevre'\nDid you mean 'other serve --prod'?",
		},
		{
			ExecutedLine: "tool sevre",
			ExitCode:     1,
			StderrTail:   "unknown subcommand 'other'\nDid you mean 'serve'?",
		},
		{
			ExecutedLine: "tool sevre",
			ExitCode:     1,
			StderrTail:   "unknown subcommand 'sevre'\nDid you mean 'serve;rm'?",
		},
	}

	for _, params := range tests {
		if got := (Engine{}).Correct(context.Background(), fakeHost{}, params); len(got) != 0 {
			t.Fatalf("accepted unsafe diagnostic alternative %v for %+v", got, params)
		}
	}
}

func TestDiagnosticAlternativeDoesNotAuthorizeDestructiveExecutable(t *testing.T) {
	got := (Engine{}).Correct(context.Background(), fakeHost{}, Params{
		ExecutedLine: "rmm -rf build",
		ExitCode:     127,
		FailureKind:  "command_not_found",
		StderrTail:   "rmm: command not found\nDid you mean 'rm'?",
	})
	if len(got) != 0 {
		t.Fatalf("accepted destructive diagnostic-only correction: %v", got)
	}
}

func TestDiagnosticAlternativeOutranksUnrelatedLocalEvidence(t *testing.T) {
	h := fakeHost{
		history:    []HistoryEntry{{Line: "tool severe"}},
		completion: []CompletionItem{{InsertText: "severe"}},
	}
	got := (Engine{}).Correct(context.Background(), h, Params{
		ExecutedLine: "tool sevre",
		ExitCode:     1,
		StderrTail:   "unknown subcommand 'sevre'\ntip: a similar subcommand exists: 'serve'",
	})
	if len(got) != 1 || got[0] != "tool serve" {
		t.Fatalf("got %v, want the diagnostic-provided correction", got)
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
