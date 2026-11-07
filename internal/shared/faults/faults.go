// Package faults provides common classification of technical failures.
package faults

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"github.com/artarts36/envmasker"
)

const maxDiagnosticLength = 4000

var embeddedSecretToken = regexp.MustCompile(
	`(?i)\b(?:[[:alnum:]]+[-_.])+(?:secret|token|password|credential|api[_-]?key)(?:[-_.][[:alnum:]]+)*\b`,
)

var diagnosticCredential = regexp.MustCompile(
	`(?i)((?:^|[^[:alnum:]_.-])(?:--?)?(?:password[\t ]*:|` +
		`(?:password|passwd|secret|token|credential|api[_-]?key)[\t ]*=)[\t ]*)` +
		`("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^\s'"<>&#]+)`,
)

var diagnosticField = regexp.MustCompile(
	`((?:^|[\t ])([[:alnum:]_.-]+)[\t ]*=[\t ]*)("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^\s'"<>]+)`,
)

var diagnosticAuthorization = regexp.MustCompile(
	`(?i)(\bauthorization[\t ]*[:=][\t ]*)` +
		`("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|(?:bearer|basic)[\t ]+[^\s'"<>]+|\*{4})`,
)

// Give envmasker individual tokens, leading credential fields, and explicit CLI
// flags. In the middle of prose, adjacent words such as "secret file" are ambiguous.
var diagnosticToken = regexp.MustCompile(
	`(?i)(?:^|\S*--?)(?:password|pass|pwd|secret|token|api-key|apikey|dsn|` +
		`database-url|db-url|redis-url|mongo-uri|mongodb-uri)[\t ]+[^\s'"<>&#,;)\]}]+|\S+`,
)

// Require a host boundary so image references such as image:tag@sha256:digest are kept.
var bareDSNUserInfo = regexp.MustCompile(
	`\b[[:alnum:]_.%+-]+:[^\s/@?'#"<>()]+(@[[:alnum:]_.-]+(?::[0-9]{1,5})?(?::?(?:$|\s)|[/?"'<>),;\]]))`,
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
// Masking recognizes specific formats and may miss unstructured credentials.
func SafeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	diagnostic := strings.TrimSpace(err.Error())
	if diagnostic == "" {
		return ""
	}
	diagnostic = bareDSNUserInfo.ReplaceAllString(diagnostic, envmasker.MaskValue+":"+envmasker.MaskValue+"${1}")
	diagnostic = diagnosticAuthorization.ReplaceAllString(diagnostic, "${1}"+envmasker.MaskValue)
	diagnostic = diagnosticField.ReplaceAllStringFunc(diagnostic, func(field string) string {
		parts := diagnosticField.FindStringSubmatch(field)
		if _, sensitive := envmasker.Mask(parts[2], envmasker.MaskValue); sensitive {
			return parts[1] + envmasker.MaskValue
		}
		return field
	})
	diagnostic = diagnosticCredential.ReplaceAllString(diagnostic, "${1}"+envmasker.MaskValue)
	diagnostic = diagnosticToken.ReplaceAllStringFunc(diagnostic, func(token string) string {
		masked, _ := envmasker.Mask("", token)
		return masked
	})
	diagnostic = embeddedSecretToken.ReplaceAllString(diagnostic, envmasker.MaskValue)
	if len(diagnostic) > maxDiagnosticLength {
		diagnostic = diagnostic[:maxDiagnosticLength] + "…"
	}
	return diagnostic
}

// SafeError copies only the safe diagnostic and retry classification, without
// retaining or unwrapping the original error, which may contain secrets.
func SafeError(err error) error {
	if err == nil {
		return nil
	}
	var classified Error
	temporary := errors.As(err, &classified) && classified.Temporary()
	return safeError{diagnostic: SafeDiagnostic(err), temporary: temporary}
}

type safeError struct {
	diagnostic string
	temporary  bool
}

func (e safeError) Error() string   { return e.diagnostic }
func (e safeError) Temporary() bool { return e.temporary }

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
