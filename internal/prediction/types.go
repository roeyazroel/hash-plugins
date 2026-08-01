package prediction

import "time"

type Outcome struct {
	Line, CWD   string
	ExitCode    int
	Canceled    bool
	FailureKind string
}
type Previous struct {
	Line, CWD string
	ExitCode  int
	Canceled  bool
}
type transition struct {
	Previous, Next, CWD      string
	HashCount, ImportedCount int
	LastUsed                 time.Time
}

func validLine(line string) bool { return safeCommand(line) == nil }
func success(o Outcome) bool {
	return o.Line != "" && o.ExitCode == 0 && !o.Canceled && o.FailureKind == "" && validLine(o.Line)
}
