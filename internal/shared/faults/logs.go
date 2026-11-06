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
var unstructuredSecret = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])secret(?:$|[^[:alnum:]]|key\b|value\b)`)

// Accept false positives for standalone Base64: a log tail can omit both PEM markers.
// Match long fragments and padded tails; short unpadded words are ordinary diagnostics too.
var pemKeyFragment = regexp.MustCompile(`^(?:[A-Za-z0-9+/]{16,}={0,2}|[A-Za-z0-9+/]{2,}={1,2})$`)
var pemBase64Line = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

// DiagnosticLogLines masks credentials and bounds sanitized log content.
// Limits include newline separators; callers can share a budget across log streams.
func DiagnosticLogLines(logs []string, maxLines, maxBytes int) []string {
	logLines := strings.Split(strings.Join(logs, "\n"), "\n")
	removePEMKeyFragments(logLines)
	var lines []string
	used := 0
	privateKey := false
	for _, line := range logLines {
		remaining := maxBytes - used - len(lines)
		if remaining <= 0 || len(lines) >= maxLines {
			break
		}
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "-----BEGIN") && strings.Contains(upper, "PRIVATE KEY-----") {
			privateKey = true
		}
		if strings.Contains(upper, "-----END") && strings.Contains(upper, "PRIVATE KEY-----") {
			privateKey = false
			continue
		}
		if privateKey {
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

// Remove entire Base64 runs when a long/padded fragment or an adjacent END marker
// suggests a truncated PEM body. Short unpadded diagnostic words alone stay visible.
func removePEMKeyFragments(lines []string) {
	start := 0
	suspected := false
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if pemBase64Line.MatchString(line) {
			suspected = suspected || pemKeyFragment.MatchString(line)
			continue
		}
		upper := strings.ToUpper(line)
		keyEnd := strings.Contains(upper, "-----END") && strings.Contains(upper, "PRIVATE KEY-----")
		if suspected || keyEnd {
			clear(lines[start:i])
		}
		start = i + 1
		suspected = false
	}
	if suspected {
		clear(lines[start:])
	}
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
	if unstructuredSecret.MatchString(value) {
		return true
	}
	for _, marker := range []string{
		"password", "passwd", "token", "credential", "authorization", "bearer",
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
