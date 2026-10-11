// Package faults provides common classification of technical failures.
package faults

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/artarts36/envmasker"
)

const maxDiagnosticLength = 4000

var embeddedSecretToken = regexp.MustCompile(
	`(?i)\b(?:[[:alnum:]]+[-_.])+(?:secret|token|password|credential|api[_-]?key)(?:[-_.][[:alnum:]]+)*\b`,
)

// Error is an error with a technical classification.
type Error interface {
	error

	// Temporary reports whether the failure may disappear on retry.
	Temporary() bool
}

// SafeDiagnostic returns bounded, secret-masked technical context suitable for
// persistence and user-facing diagnostics. An empty error remains empty so
// callers can distinguish unavailable detail from a fabricated explanation.
func SafeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	diagnostic := strings.TrimSpace(err.Error())
	if diagnostic == "" {
		return ""
	}
	diagnostic, _ = envmasker.Mask("", diagnostic)
	diagnostic = embeddedSecretToken.ReplaceAllString(diagnostic, envmasker.MaskValue)
	if len(diagnostic) > maxDiagnosticLength {
		diagnostic = diagnostic[:maxDiagnosticLength] + "…"
	}
	return diagnostic
}

// NewSafeLogger wraps a logger so error-valued attributes are masked before
// they reach any output handler.
func NewSafeLogger(logger *slog.Logger) *slog.Logger {
	return slog.New(&safeHandler{next: logger.Handler()})
}

type safeHandler struct {
	next slog.Handler
}

func (h *safeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *safeHandler) Handle(ctx context.Context, record slog.Record) error {
	safe := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		safe.AddAttrs(safeAttr(attr))
		return true
	})
	return h.next.Handle(ctx, safe)
}

func (h *safeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	safe := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		safe = append(safe, safeAttr(attr))
	}
	return &safeHandler{next: h.next.WithAttrs(safe)}
}

func (h *safeHandler) WithGroup(name string) slog.Handler {
	return &safeHandler{next: h.next.WithGroup(name)}
}

func safeAttr(attr slog.Attr) slog.Attr {
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindAny {
		if err, ok := value.Any().(error); ok {
			return slog.String(attr.Key, SafeDiagnostic(err))
		}
	}
	if value.Kind() == slog.KindGroup {
		children := value.Group()
		for i := range children {
			children[i] = safeAttr(children[i])
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(children...)}
	}
	return slog.Attr{Key: attr.Key, Value: value}
}
