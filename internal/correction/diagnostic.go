package correction

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type diagnosticKind uint8

const (
	diagnosticNone diagnosticKind = iota
	diagnosticExecutable
	diagnosticSubcommand
	diagnosticLongFlag
)

var diagnosticPatterns = []struct {
	kind    diagnosticKind
	pattern *regexp.Regexp
}{
	{diagnosticSubcommand, regexp.MustCompile(`(?i)['\"]([[:alnum:]_.-]+)['\"][[:space:]]+is[[:space:]]+not[[:space:]]+(?:an?[[:space:]]+)?(?:valid[[:space:]]+)?(?:[[:alnum:]_.-]+[[:space:]]+)?(?:command|subcommand)`)},
	{diagnosticExecutable, regexp.MustCompile(`(?i)(?:unknown|unrecognized|invalid) command[^[:alnum:]-]+['\"]?([[:alnum:]_.-]+)`)},
	{diagnosticSubcommand, regexp.MustCompile(`(?i)(?:unknown|unrecognized|invalid) subcommand[^[:alnum:]-]+['\"]?([[:alnum:]_.-]+)`)},
	{diagnosticLongFlag, regexp.MustCompile(`(?i)(?:unknown|unrecognized|invalid) (?:option|flag)[^[:alnum:]-]+['\"]?([[:alnum:]_.-]+)`)},
	{diagnosticLongFlag, regexp.MustCompile(`(?i)flag provided but not defined:[[:space:]]+([[:alnum:]_.-]+)`)},
	{diagnosticLongFlag, regexp.MustCompile(`(?i)unexpected (?:argument|option)[^[:alnum:]-]+['\"]?((?:--)[[:alnum:]_.-]+)`)},
}

var diagnosticAlternativePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:did[[:space:]]+you[[:space:]]+mean(?:[[:space:]]+(?:this|one[[:space:]]+of[[:space:]]+these))?|perhaps[[:space:]]+you[[:space:]]+meant|maybe[[:space:]]+you[[:space:]]+meant)[[:space:]]*[?:=-]?[[:space:]]*(.*)$`),
	regexp.MustCompile(`(?i)(?:the[[:space:]]+)?most[[:space:]]+similar[[:space:]]+(?:command|subcommand|option|flag|argument)s?[[:space:]]+(?:is|are)[[:space:]]*:?[[:space:]]*(.*)$`),
	regexp.MustCompile(`(?i)a[[:space:]]+similar[[:space:]]+(?:command|subcommand|option|flag|argument)[[:space:]]+exists(?:[[:space:]]+named)?[[:space:]]*:?[[:space:]]*(.*)$`),
}

func diagnosticToken(text string) (diagnosticKind, string) {
	for _, pattern := range diagnosticPatterns {
		if match := pattern.pattern.FindStringSubmatch(text); len(match) == 2 {
			return pattern.kind, strings.Trim(match[1], "'\"")
		}
	}
	return diagnosticNone, ""
}

func diagnosticAlternatives(text string) []string {
	const maxDiagnosticBytes = 10 * 1024
	if len(text) > maxDiagnosticBytes {
		text = text[len(text)-maxDiagnosticBytes:]
	}
	lines := strings.Split(text, "\n")
	seen := make(map[string]bool)
	var alternatives []string
	appendAlternative := func(raw string) {
		value := normalizeDiagnosticAlternative(raw)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		alternatives = append(alternatives, value)
	}

	for lineIndex, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, pattern := range diagnosticAlternativePatterns {
			match := pattern.FindStringSubmatch(trimmed)
			if len(match) != 2 {
				continue
			}
			if strings.TrimSpace(match[1]) != "" {
				appendAlternative(match[1])
				continue
			}
			collectIndentedAlternatives(lines, lineIndex+1, appendAlternative)
		}
	}
	return alternatives
}

func collectIndentedAlternatives(lines []string, start int, appendAlternative func(string)) {
	collected := 0
	for next := start; next < len(lines) && collected < 5; next++ {
		line := lines[next]
		if strings.TrimSpace(line) == "" {
			if collected > 0 {
				return
			}
			continue
		}
		if !isIndentedAlternative(line) {
			return
		}
		appendAlternative(line)
		collected++
	}
}

func isIndentedAlternative(line string) bool {
	if line == "" {
		return false
	}
	if line[0] == ' ' || line[0] == '\t' {
		return true
	}
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ")
}

func normalizeDiagnosticAlternative(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "- ") || strings.HasPrefix(raw, "* ") {
		raw = strings.TrimSpace(raw[2:])
	}
	raw = strings.Trim(raw, "`'\"“”‘’()[]{}.,:;!?")
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 256 || !utf8.ValidString(raw) {
		return ""
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return raw
}

func diagnosticReplacement(alternative string, command []shellToken, index int) (string, bool) {
	if words, valid := parseStaticSimple(alternative); valid && len(words) == len(command) {
		for i := range words {
			if i != index && words[i].value != command[i].value {
				return "", false
			}
		}
		if validDiagnosticCandidate(words[index].value) {
			return words[index].value, true
		}
		return "", false
	}
	if strings.ContainsAny(alternative, " \t") || !validDiagnosticCandidate(alternative) {
		return "", false
	}
	return alternative, true
}

func validDiagnosticCandidate(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	hasWordRune := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			hasWordRune = true
			continue
		}
		if r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return hasWordRune
}
