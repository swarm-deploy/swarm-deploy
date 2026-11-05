package faults

import (
	"errors"
	"regexp"
	"strings"

	"github.com/artarts36/envmasker"
)

const (
	// MaxDiagnosticLogLines bounds log lines in deployment diagnostics.
	MaxDiagnosticLogLines = 20
	// MaxDiagnosticLogBytes bounds log content in deployment diagnostics.
	MaxDiagnosticLogBytes = 1600
)

var maskedDiagnosticURL = regexp.MustCompile(`[[:alpha:]][[:alnum:]+.-]*://\*{4}(?::\*{4}@[^\s'"<>]+)?`)

// DiagnosticLogLines masks credentials and bounds sanitized log content.
// Limits include newline separators; callers can share a budget across log streams.
func DiagnosticLogLines(logs []string, maxLines, maxBytes int) []string {
	var lines []string
	used := 0
	privateKey := false
	for _, line := range strings.Split(strings.Join(logs, "\n"), "\n") {
		remaining := maxBytes - used - len(lines)
		if remaining <= 0 || len(lines) >= maxLines {
			break
		}
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "-----BEGIN") && strings.Contains(upper, "PRIVATE KEY-----") {
			privateKey = true
		}
		if privateKey {
			if strings.Contains(upper, "-----END") && strings.Contains(upper, "PRIVATE KEY-----") {
				privateKey = false
			}
			continue
		}
		line = SafeDiagnostic(errors.New(line))
		if line == "" || mayContainCredentials(line) {
			continue
		}
		if len(line) > remaining {
			line = line[:remaining]
		}
		lines = append(lines, line)
		used += len(line)
	}
	return lines
}

func mayContainCredentials(line string) bool {
	// Ignore fields and URLs whose sensitive values have already been masked.
	// Keep inspecting other field values for unstructured credentials.
	line = diagnosticAuthorization.ReplaceAllString(line, "")
	line = diagnosticCredential.ReplaceAllString(line, "")
	line = diagnosticField.ReplaceAllStringFunc(line, func(field string) string {
		value := diagnosticField.FindStringSubmatch(field)[3]
		if strings.Trim(value, `"'`) == envmasker.MaskValue {
			return ""
		}
		return field
	})
	line = maskedDiagnosticURL.ReplaceAllString(line, "")
	value := strings.ToLower(line)
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "credential", "authorization", "bearer",
		"private_key", "private-key", "api_key", "api-key", "apikey", "dsn", "database_url", "db_url",
		"database-url", "db-url", "connection_string", "connection-string", "postgres://", "postgresql://",
		"://", "-----begin",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
