package correction

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

type Params struct {
	ExecutedLine string `json:"executed_line"`
	ExitCode     int    `json:"exit_code"`
	FailureKind  string `json:"failure_kind"`
	ErrorMessage string `json:"error_message"`
	StderrTail   string `json:"stderr_tail"`
	CWD          string `json:"cwd"`
	Canceled     bool   `json:"canceled"`
}
type HistoryEntry struct {
	Line, CWD string
	ExitCode  int
	Timestamp string
}
type CompletionItem struct{ Label, InsertText string }
type Host interface {
	History(context.Context, string, string, int) ([]HistoryEntry, error)
	Completion(context.Context, string, int) ([]CompletionItem, error)
}

type Config struct {
	Strategies    []string `json:"strategies"`
	HistoryLimit  int      `json:"history_limit"`
	MaxCandidates int      `json:"max_candidates"`
}

func DefaultConfig() Config {
	return Config{
		Strategies:    []string{"executable", "subcommand", "long_flag"},
		HistoryLimit:  100,
		MaxCandidates: 3,
	}
}

// ParseConfig overlays user settings on the production-safe defaults.
func ParseConfig(raw json.RawMessage) (Config, error) {
	cfg := DefaultConfig()
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return cfg, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Config{}, fmt.Errorf("decode settings: %w", err)
	}
	for name, value := range fields {
		switch name {
		case "strategies":
			if err := json.Unmarshal(value, &cfg.Strategies); err != nil {
				return Config{}, fmt.Errorf("strategies: %w", err)
			}
		case "history_limit":
			if err := json.Unmarshal(value, &cfg.HistoryLimit); err != nil {
				return Config{}, fmt.Errorf("history_limit: %w", err)
			}
		case "max_candidates":
			if err := json.Unmarshal(value, &cfg.MaxCandidates); err != nil {
				return Config{}, fmt.Errorf("max_candidates: %w", err)
			}
		default:
			return Config{}, fmt.Errorf("unknown setting %q", name)
		}
	}
	if cfg.HistoryLimit < 1 || cfg.HistoryLimit > 500 {
		return Config{}, fmt.Errorf("history_limit must be between 1 and 500")
	}
	if cfg.MaxCandidates < 1 || cfg.MaxCandidates > 5 {
		return Config{}, fmt.Errorf("max_candidates must be between 1 and 5")
	}
	if len(cfg.Strategies) == 0 {
		return Config{}, fmt.Errorf("strategies must not be empty")
	}
	seen := make(map[string]bool, len(cfg.Strategies))
	for _, strategy := range cfg.Strategies {
		if strategy != "executable" && strategy != "subcommand" && strategy != "long_flag" {
			return Config{}, fmt.Errorf("unknown strategy %q", strategy)
		}
		if seen[strategy] {
			return Config{}, fmt.Errorf("duplicate strategy %q", strategy)
		}
		seen[strategy] = true
	}
	return cfg, nil
}

type Engine struct {
	HistoryLimit  int
	MaxCandidates int
	Strategies    []string
}

type candidateEvidence struct {
	sources int
	recency int
}

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
	{diagnosticSubcommand, regexp.MustCompile(`(?i)git:[[:space:]]+['\"]([[:alnum:]_.-]+)['\"][[:space:]]+is not a git command`)},
	{diagnosticExecutable, regexp.MustCompile(`(?i)(?:unknown|unrecognized|invalid) command[^[:alnum:]-]+['\"]?([[:alnum:]_.-]+)`)},
	{diagnosticSubcommand, regexp.MustCompile(`(?i)(?:unknown|unrecognized|invalid) subcommand[^[:alnum:]-]+['\"]?([[:alnum:]_.-]+)`)},
	{diagnosticLongFlag, regexp.MustCompile(`(?i)(?:unknown|unrecognized|invalid) (?:option|flag)[^[:alnum:]-]+['\"]?([[:alnum:]_.-]+)`)},
	{diagnosticLongFlag, regexp.MustCompile(`(?i)flag provided but not defined:[[:space:]]+([[:alnum:]_.-]+)`)},
	{diagnosticLongFlag, regexp.MustCompile(`(?i)unexpected (?:argument|option)[^[:alnum:]-]+['\"]?((?:--)[[:alnum:]_.-]+)`)},
}

func (e Engine) Correct(ctx context.Context, host Host, p Params) []string {
	if p.ExitCode == 0 || p.Canceled || p.FailureKind == "interrupted" || p.FailureKind == "signal" || !safeLine(p.ExecutedLine) {
		return nil
	}
	words, ok := parseStaticSimple(p.ExecutedLine)
	if !ok || len(words) == 0 || unsafeWrapper(words[0].value) {
		return nil
	}
	index := -1
	if p.FailureKind == "command_not_found" {
		index = 0
	} else {
		diagnostic := p.ErrorMessage + "\n" + p.StderrTail
		kind, unknown := diagnosticToken(diagnostic)
		matches := 0
		for i, word := range words {
			if word.value == unknown && i > 0 {
				index = i
				matches++
			}
		}
		if matches != 1 {
			return nil
		}
		target := words[index].value
		if kind == diagnosticSubcommand && (index != 1 || strings.HasPrefix(target, "-")) {
			return nil
		}
		if kind == diagnosticLongFlag && !strings.HasPrefix(target, "--") {
			return nil
		}
		if kind == diagnosticExecutable && (index != 1 || strings.HasPrefix(target, "-")) {
			return nil
		}
	}
	if index < 0 {
		return nil
	}
	target := words[index].value
	if strings.HasPrefix(target, "-") && !strings.HasPrefix(target, "--") {
		return nil
	}
	if index > 1 && !strings.HasPrefix(target, "--") {
		return nil
	}
	strategy := "subcommand"
	if index == 0 {
		strategy = "executable"
	} else if strings.HasPrefix(target, "--") {
		strategy = "long_flag"
	}
	if len(e.Strategies) > 0 && !contains(e.Strategies, strategy) {
		return nil
	}
	prefix := ""
	if index > 0 {
		prefix = p.ExecutedLine[:words[index].start]
	}
	limit := e.HistoryLimit
	if limit < 1 || limit > 500 {
		limit = 100
	}
	queryLimit := min(limit, 100)
	entries, _ := host.History(ctx, strings.TrimSpace(prefix), p.CWD, queryLimit)
	items, _ := host.Completion(ctx, prefix, len(prefix))
	sources := map[string]candidateEvidence{}
	for historyIndex, entry := range entries {
		ws, valid := parseStaticSimple(entry.Line)
		if valid && len(ws) > index {
			addCandidate(sources, target, ws[index].value, 1, len(entries)-historyIndex, index)
		}
	}
	for _, item := range items {
		addCandidate(sources, target, item.InsertText, 2, 0, index)
	}
	type ranked struct {
		value                      string
		sources, distance, recency int
	}
	var rankedCandidates []ranked
	for value, candidateEvidence := range sources {
		if index == 0 && destructiveExecutable(value) && candidateEvidence.sources != 3 {
			continue
		}
		rankedCandidates = append(rankedCandidates, ranked{value, candidateEvidence.sources, damerau(strings.ToLower(target), strings.ToLower(value)), candidateEvidence.recency})
	}
	sort.Slice(rankedCandidates, func(i, j int) bool {
		ai, aj := rankedCandidates[i], rankedCandidates[j]
		if sourceCount(ai.sources) != sourceCount(aj.sources) {
			return sourceCount(ai.sources) > sourceCount(aj.sources)
		}
		if ai.distance != aj.distance {
			return ai.distance < aj.distance
		}
		if ai.recency != aj.recency {
			return ai.recency > aj.recency
		}
		return ai.value < aj.value
	})
	if len(rankedCandidates) == 0 {
		return nil
	}
	best := rankedCandidates[0]
	max := e.MaxCandidates
	if max < 1 || max > 5 {
		max = 3
	}
	var out []string
	for _, candidate := range rankedCandidates {
		if sourceCount(candidate.sources) != sourceCount(best.sources) || candidate.distance != best.distance || len(out) >= max {
			break
		}
		replacement := quoteLike(words[index].raw, candidate.value)
		out = append(out, p.ExecutedLine[:words[index].start]+replacement+p.ExecutedLine[words[index].end:])
	}
	return out
}

func destructiveExecutable(value string) bool {
	switch strings.ToLower(value) {
	case "rm", "dd", "mkfs", "shutdown", "reboot", "kill", "pkill":
		return true
	default:
		return false
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func safeLine(line string) bool {
	return utf8.ValidString(line) && !strings.ContainsAny(line, "\r\n")
}

type shellToken struct {
	raw, value string
	start, end int
}

// parseStaticSimple is deliberately smaller than a shell parser. It accepts
// only one static simple command, while retaining byte spans so a correction
// can replace one token without rewriting the user's input.
func parseStaticSimple(line string) ([]shellToken, bool) {
	var args []shellToken
	commandSeen := false
	expectRedirection := false
	for i := 0; i < len(line); {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		if strings.ContainsRune("|;&`(){}[]$", rune(line[i])) {
			return nil, false
		}
		if line[i] == '<' || line[i] == '>' {
			expectRedirection = true
			i++
			if i < len(line) && (line[i] == '<' || line[i] == '>' || line[i] == '&' || line[i] == '|') {
				i++
			}
			continue
		}
		start := i
		var value strings.Builder
		quote := byte(0)
		for i < len(line) {
			c := line[i]
			if quote == 0 {
				if c == ' ' || c == '\t' || c == '<' || c == '>' {
					break
				}
				if strings.ContainsRune("|;&`(){}[]$", rune(c)) {
					return nil, false
				}
				if c == '\'' || c == '"' {
					quote = c
					i++
					continue
				}
				if c == '\\' {
					if i+1 >= len(line) {
						return nil, false
					}
					i++
					value.WriteByte(line[i])
					i++
					continue
				}
				value.WriteByte(c)
				i++
				continue
			}
			if c == quote {
				quote = 0
				i++
				continue
			}
			if quote == '"' && (c == '$' || c == '`' || c == '\\') {
				return nil, false
			}
			value.WriteByte(c)
			i++
		}
		if quote != 0 || i == start {
			return nil, false
		}
		token := shellToken{raw: line[start:i], value: value.String(), start: start, end: i}
		if expectRedirection {
			expectRedirection = false
			continue
		}
		if !commandSeen && isAssignment(token.value) {
			continue
		}
		if i < len(line) && (line[i] == '<' || line[i] == '>') && allDigits(token.value) {
			continue
		}
		commandSeen = true
		args = append(args, token)
	}
	return args, !expectRedirection && len(args) > 0
}

func isAssignment(value string) bool {
	name, _, ok := strings.Cut(value, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func quoteLike(raw, value string) string {
	if len(raw) >= 2 && raw[0] == raw[len(raw)-1] && (raw[0] == '\'' || raw[0] == '"') {
		return string(raw[0]) + value + string(raw[0])
	}
	return value
}
func diagnosticToken(text string) (diagnosticKind, string) {
	for _, p := range diagnosticPatterns {
		if m := p.pattern.FindStringSubmatch(text); len(m) == 2 {
			return p.kind, strings.Trim(m[1], "'\"")
		}
	}
	return diagnosticNone, ""
}

func unsafeWrapper(command string) bool {
	switch command {
	case "sudo", "doas", "eval", "env", "command", "builtin", "exec", "xargs":
		return true
	default:
		return false
	}
}
func sourceCount(mask int) int {
	if mask == 3 {
		return 2
	}
	if mask != 0 {
		return 1
	}
	return 0
}

func addCandidate(dest map[string]candidateEvidence, target, value string, source, recency, index int) {
	value = strings.TrimSpace(value)
	if value == "" || value == target || strings.ContainsAny(value, " \t\r\n") {
		return
	}
	if index > 0 && strings.HasPrefix(target, "--") != strings.HasPrefix(value, "--") {
		return
	}
	d := damerau(strings.ToLower(target), strings.ToLower(value))
	n := utf8.RuneCountInString(target)
	allowed := 1
	if n >= 5 {
		allowed = 2
	}
	if n > 8 {
		allowed = 3
		if float64(d)/float64(n) > 0.34 {
			return
		}
	}
	if d > allowed {
		return
	}
	evidence := dest[value]
	evidence.sources |= source
	if recency > evidence.recency {
		evidence.recency = recency
	}
	dest[value] = evidence
}
func damerau(a, b string) int {
	ar, br := []rune(a), []rune(b)
	d := make([][]int, len(ar)+1)
	for i := range d {
		d[i] = make([]int, len(br)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ar); i++ {
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ar)][len(br)]
}
