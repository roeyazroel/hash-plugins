package autosuggestion

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"
)

const maxSuggestionBytes = 16 * 1024

var sensitiveMarkers = []string{
	"password", "passwd", "api_key", "api-key", "apikey", "access_token",
	"auth_token", "authorization:", "authorization=", "bearer ", "client_secret",
	"private key", "private_key", "secret_key", "token", "aws_access_key_id",
	"aws_secret_access_key", "aws_session_token",
}

func safeStoredCommand(command string) bool {
	if command == "" || len(command) > maxSuggestionBytes || !utf8.ValidString(command) || strings.TrimSpace(command) == "" {
		return false
	}
	if strings.ContainsAny(command, "\r\n") || strings.IndexFunc(command, unicode.IsControl) >= 0 {
		return false
	}
	lower := strings.ToLower(command)
	for _, marker := range sensitiveMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	if strings.Contains(lower, "curl -u ") || strings.Contains(lower, "curl --user ") || hasURLUserInfo(lower) {
		return false
	}
	return true
}

func hasURLUserInfo(line string) bool {
	for searchFrom := 0; searchFrom < len(line); {
		offset := strings.Index(line[searchFrom:], "://")
		if offset < 0 {
			return false
		}
		authorityStart := searchFrom + offset + 3
		authorityEnd := len(line)
		if slash := strings.IndexAny(line[authorityStart:], "/?# "); slash >= 0 {
			authorityEnd = authorityStart + slash
		}
		authority := line[authorityStart:authorityEnd]
		if at := strings.LastIndexByte(authority, '@'); at > 0 && strings.Contains(authority[:at], ":") {
			return true
		}
		searchFrom = authorityStart
	}
	return false
}

func validCandidate(prefix, candidate, dialect string) bool {
	// Most imported commands do not match the current prefix. Reject them
	// before Unicode, credential, and syntax validation to keep editor latency
	// bounded even with three full 50,000-command imports.
	if candidate == prefix || !strings.HasPrefix(candidate, prefix) || !safeStoredCommand(candidate) {
		return false
	}
	if dialect != "" && !strings.EqualFold(dialect, "bash") && !strings.EqualFold(dialect, "zsh") {
		return false
	}
	variant := syntax.LangBash
	if strings.EqualFold(dialect, "zsh") {
		variant = syntax.LangZsh
	}
	file, err := syntax.NewParser(syntax.Variant(variant)).Parse(strings.NewReader(candidate), "suggestion")
	return err == nil && len(file.Stmts) > 0
}
