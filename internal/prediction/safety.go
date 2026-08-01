package prediction

import (
	"errors"
	"strings"
	"unicode/utf8"
)

func safeCommand(line string) error {
	if line == "" || len(line) > 16*1024 || !utf8.ValidString(line) {
		return errors.New("invalid command")
	}
	for _, r := range line {
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return errors.New("invalid command")
		}
	}
	var single, double, escaped bool
	for _, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && !single {
			escaped = true
			continue
		}
		if r == '\'' && !double {
			single = !single
		}
		if r == '"' && !single {
			double = !double
		}
	}
	if single || double || escaped {
		return errors.New("invalid command")
	}
	lower := strings.ToLower(line)
	for _, marker := range []string{"password=", "passwd=", "api_key=", "api-key=", "token=", "secret=", "private_key=", "authorization:", "bearer ", "ghp_"} {
		if strings.Contains(lower, marker) {
			return errors.New("sensitive command")
		}
	}
	return nil
}
