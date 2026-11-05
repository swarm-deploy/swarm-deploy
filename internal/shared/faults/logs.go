package faults

import (
	"errors"
	"strings"
)

const (
	// MaxDiagnosticLogLines bounds log lines in deployment diagnostics.
	MaxDiagnosticLogLines = 20
	// MaxDiagnosticLogBytes bounds log content in deployment diagnostics.
	MaxDiagnosticLogBytes = 1600
)

// DiagnosticLogLines filters credential-bearing lines and bounds sanitized log content.
// Limits include newline separators; callers can share a budget across log streams.
func DiagnosticLogLines(logs []string, maxLines, maxBytes int) []string {
	var lines []string
	used := 0
	for _, raw := range logs {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || mayContainCredentials(line) {
				continue
			}
			line = SafeDiagnostic(errors.New(line))
			remaining := maxBytes - used - len(lines)
			if line == "" || remaining <= 0 || len(lines) >= maxLines {
				break
			}
			if len(line) > remaining {
				line = line[:remaining]
			}
			lines = append(lines, line)
			used += len(line)
		}
		if used+len(lines) >= maxBytes || len(lines) >= maxLines {
			break
		}
	}
	return lines
}

func mayContainCredentials(line string) bool {
	value := strings.ToLower(line)
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "credential", "authorization", "bearer",
		"private_key", "api_key", "apikey", "dsn", "database_url", "postgres://", "postgresql://",
		"://", "-----begin", "=",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
