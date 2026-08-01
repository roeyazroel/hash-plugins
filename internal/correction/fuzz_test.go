package correction

import (
	"strings"
	"testing"
	"unicode"
)

func FuzzParseStaticSimple(f *testing.F) {
	for _, seed := range []string{"git status", "FOO=bar  git 'sttaus' >out", "git x | sh", "echo $(id)", "tool --flag"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		tokens, ok := parseStaticSimple(line)
		if !ok {
			return
		}
		previousEnd := 0
		for _, token := range tokens {
			if token.start < previousEnd || token.start < 0 || token.end > len(line) || token.start >= token.end {
				t.Fatalf("invalid token span %+v for %q", token, line)
			}
			previousEnd = token.end
		}
	})
}

func FuzzDamerauSymmetry(f *testing.F) {
	f.Add("sttaus", "status")
	f.Add("界", "会")
	f.Fuzz(func(t *testing.T, a, b string) {
		ab, ba := damerau(a, b), damerau(b, a)
		if ab < 0 || ab != ba {
			t.Fatalf("distance %q/%q = %d/%d", a, b, ab, ba)
		}
	})
}

func FuzzDiagnosticAlternatives(f *testing.F) {
	for _, seed := range []string{
		"Did you mean 'serve'?",
		"The most similar command is\n\tpull",
		"tip: a similar flag exists: '--verbose'",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, diagnostic string) {
		for _, alternative := range diagnosticAlternatives(diagnostic) {
			if alternative == "" || len(alternative) > 256 || strings.ContainsAny(alternative, "\r\n") {
				t.Fatalf("invalid diagnostic alternative %q", alternative)
			}
			for _, r := range alternative {
				if unicode.IsControl(r) {
					t.Fatalf("control character in diagnostic alternative %q", alternative)
				}
			}
		}
	})
}
