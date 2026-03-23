package assistant

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	errPromptInjection = errors.New("request rejected by prompt injection guard")

	defaultGuardPatterns = []string{
		`(?i)\bignore\b.{0,80}\b(previous|system|developer)\b.{0,80}\binstruction`,
		`(?i)\b(reveal|show|print|dump)\b.{0,80}\b(system prompt|developer message|hidden prompt)\b`,
		`(?i)\b(bypass|disable|override)\b.{0,80}\b(guard|security|safety|policy)\b`,
		`(?i)\b(tool|function)\b.{0,80}\bcall\b.{0,80}\b(sync|list_history_events)\b`,
	}
)

type promptGuard struct {
	patterns []*regexp.Regexp
}

func newPromptGuard() (*promptGuard, error) {
	patterns := make([]*regexp.Regexp, 0, len(defaultGuardPatterns))
	for _, pattern := range defaultGuardPatterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile prompt guard pattern %q: %w", pattern, err)
		}
		patterns = append(patterns, compiled)
	}

	return &promptGuard{
		patterns: patterns,
	}, nil
}

func (g *promptGuard) validate(message string) error {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return nil
	}

	for _, pattern := range g.patterns {
		if pattern.MatchString(trimmed) {
			return errPromptInjection
		}
	}

	return nil
}
