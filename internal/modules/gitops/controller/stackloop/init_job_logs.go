package stackloop

import (
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

const (
	maxInitJobDiagnosticLines = 20
	maxInitJobDiagnosticBytes = 1600
)

// withInitJobLogs extracts failure output before the ephemeral Docker service
// is removed. Only conservative, bounded diagnostics may be persisted.
func withInitJobLogs(err error) (error, bool) {
	var logged interface{ Logs() []string }
	if !errors.As(err, &logged) {
		return err, false
	}
	lines := initJobDiagnosticLines(logged.Logs())
	if len(lines) == 0 {
		return err, true
	}
	return fmt.Errorf("%w\ninit job logs:\n%s", err, strings.Join(lines, "\n")), true
}

func initJobDiagnosticLines(logs []string) []string {
	var lines []string
	used := 0
	for _, raw := range logs {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || mayContainCredentials(line) {
				continue
			}
			line = faults.SafeDiagnostic(errors.New(line))
			if line == "" {
				continue
			}
			remaining := maxInitJobDiagnosticBytes - used - len(lines)
			if remaining <= 0 || len(lines) >= maxInitJobDiagnosticLines {
				break
			}
			if len(line) > remaining {
				line = line[:remaining]
			}
			lines = append(lines, line)
			used += len(line)
		}
		if used+len(lines) >= maxInitJobDiagnosticBytes || len(lines) >= maxInitJobDiagnosticLines {
			break
		}
	}
	return lines
}

// Unknown log formats are not guaranteed to be safe. Drop likely credential
// lines rather than storing their potentially unmasked values.
func mayContainCredentials(line string) bool {
	value := strings.ToLower(line)
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "credential", "authorization",
		"bearer", "private_key", "api_key", "apikey", "dsn", "database_url",
		"postgres://", "postgresql://", "://", "-----begin", "=",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
