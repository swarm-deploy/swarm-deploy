package tasklogs

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Level is a normalized structured log severity.
type Level string

const (
	// LevelTrace contains highly detailed diagnostic entries.
	LevelTrace Level = "TRACE"
	// LevelDebug contains developer-oriented diagnostic entries.
	LevelDebug Level = "DEBUG"
	// LevelInfo contains informational entries and plain-text fallbacks.
	LevelInfo Level = "INFO"
	// LevelWarn contains warning entries.
	LevelWarn Level = "WARN"
	// LevelError contains error entries.
	LevelError Level = "ERROR"
	// LevelFatal contains fatal or panic entries.
	LevelFatal Level = "FATAL"
)

var levels = []Level{LevelTrace, LevelDebug, LevelInfo, LevelWarn, LevelError, LevelFatal}

var (
	timestampKeys = []string{"timestamp", "time", "ts", "@timestamp"}
	levelKeys     = []string{"level", "severity", "severity_text", "log.level"}
	messageKeys   = []string{"message", "msg"}
)

const unixMillisecondsThreshold = 1e12

// Entry is a normalized task log entry.
type Entry struct {
	// Timestamp is the application timestamp when available, otherwise the Docker timestamp.
	Timestamp time.Time `json:"timestamp,omitempty"`
	// Stream preserves the Docker stdout or stderr stream metadata.
	Stream string `json:"stream"`
	// Level is the normalized severity.
	Level Level `json:"level,omitempty"`
	// Message is the normalized human-readable message.
	Message string `json:"message"`
	// Attributes contains structured fields not used for normalization.
	Attributes map[string]any `json:"attributes,omitempty"`
	// RawMessage is the original application log line without the Docker timestamp.
	RawMessage string `json:"raw_message,omitempty"`
}

// RawEntry is one Docker task log entry before optional application-log parsing.
type RawEntry struct {
	// Timestamp is the Docker-provided log timestamp.
	Timestamp time.Time
	// Stream is stdout or stderr.
	Stream string
	// Message is the application log line without the Docker timestamp.
	Message string
}

// Parser normalizes JSON, logfmt, and plain-text log lines.
type Parser struct{}

// NewParser creates a structured task log parser.
func NewParser() *Parser {
	return &Parser{}
}

// Levels returns normalized levels in display order.
func Levels() []Level {
	return append([]Level(nil), levels...)
}

// ParseLevel converts a user-provided severity into a normalized level.
func ParseLevel(value string) (Level, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "TRACE", "TRC":
		return LevelTrace, true
	case "DEBUG", "DBG":
		return LevelDebug, true
	case "INFO", "INFORMATION", "NOTICE":
		return LevelInfo, true
	case "WARN", "WARNING":
		return LevelWarn, true
	case "ERROR", "ERR":
		return LevelError, true
	case "FATAL", "CRITICAL", "CRIT", "PANIC", "DPANIC", "EMERGENCY", "EMERG":
		return LevelFatal, true
	default:
		return "", false
	}
}

// Parse normalizes one raw application log line.
func (p *Parser) Parse(raw RawEntry) Entry {
	entry := Entry{
		Timestamp:  raw.Timestamp.UTC(),
		Stream:     raw.Stream,
		Level:      LevelInfo,
		Message:    raw.Message,
		RawMessage: raw.Message,
	}

	if fields, ok := parseJSONFields(raw.Message); ok {
		return normalizeFields(entry, fields)
	}
	if fields, ok := parseLogfmtFields(raw.Message); ok {
		return normalizeFields(entry, fields)
	}

	return entry
}

func parseJSONFields(raw string) (map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(raw)))
	decoder.UseNumber()

	fields := map[string]any{}
	if err := decoder.Decode(&fields); err != nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}

	return fields, len(fields) > 0
}

func parseLogfmtFields(raw string) (map[string]any, bool) {
	input := strings.TrimSpace(raw)
	fields := map[string]any{}

	for len(input) > 0 {
		input = strings.TrimLeftFunc(input, unicode.IsSpace)
		if input == "" {
			break
		}

		equals := strings.IndexByte(input, '=')
		if equals <= 0 {
			return nil, false
		}
		key := input[:equals]
		if !validLogfmtKey(key) {
			return nil, false
		}
		input = input[equals+1:]

		value, rest, ok := readLogfmtValue(input)
		if !ok {
			return nil, false
		}
		fields[key] = value
		input = rest
	}

	return fields, len(fields) > 0
}

func validLogfmtKey(key string) bool {
	if key == "" {
		return false
	}
	for _, character := range key {
		if unicode.IsSpace(character) || character == '"' || character == '=' {
			return false
		}
	}

	return true
}

func readLogfmtValue(input string) (string, string, bool) {
	if input == "" {
		return "", "", true
	}
	if input[0] != '"' {
		end := strings.IndexFunc(input, unicode.IsSpace)
		if end < 0 {
			return input, "", true
		}

		return input[:end], input[end:], true
	}

	escaped := false
	for index := 1; index < len(input); index++ {
		switch {
		case escaped:
			escaped = false
		case input[index] == '\\':
			escaped = true
		case input[index] == '"':
			value, err := strconv.Unquote(input[:index+1])
			if err != nil {
				return "", "", false
			}
			rest := input[index+1:]
			if rest != "" && !unicode.IsSpace(rune(rest[0])) {
				return "", "", false
			}

			return value, rest, true
		}
	}

	return "", "", false
}

func normalizeFields(entry Entry, fields map[string]any) Entry {
	attributes := cloneFields(fields)

	if timestampValue, key, ok := findField(fields, timestampKeys); ok {
		delete(attributes, key)
		if timestamp, parsed := parseTimestamp(timestampValue); parsed {
			entry.Timestamp = timestamp
		}
	}
	if levelValue, key, ok := findField(fields, levelKeys); ok {
		delete(attributes, key)
		if level, parsed := ParseLevel(stringValue(levelValue)); parsed {
			entry.Level = level
		}
	}
	if messageValue, key, ok := findField(fields, messageKeys); ok {
		delete(attributes, key)
		entry.Message = stringValue(messageValue)
	}

	if len(attributes) > 0 {
		entry.Attributes = attributes
	}

	return entry
}

func findField(fields map[string]any, candidates []string) (any, string, bool) {
	for _, candidate := range candidates {
		for key, value := range fields {
			if strings.EqualFold(key, candidate) {
				return value, key, true
			}
		}
	}

	return nil, "", false
}

func cloneFields(fields map[string]any) map[string]any {
	cloned := make(map[string]any, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}

	return cloned
}

func parseTimestamp(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999Z07:00",
			"2006-01-02 15:04:05",
		}
		for _, layout := range layouts {
			parsed, err := time.Parse(layout, typed)
			if err == nil {
				return parsed.UTC(), true
			}
		}
	case json.Number:
		value, err := typed.Float64()
		if err == nil {
			seconds, nanoseconds := splitUnixTimestamp(value)
			return time.Unix(seconds, nanoseconds).UTC(), true
		}
	case float64:
		seconds, nanoseconds := splitUnixTimestamp(typed)
		return time.Unix(seconds, nanoseconds).UTC(), true
	}

	return time.Time{}, false
}

func splitUnixTimestamp(value float64) (int64, int64) {
	if value > unixMillisecondsThreshold {
		value /= 1000
	}
	seconds := int64(value)
	nanoseconds := int64((value - float64(seconds)) * float64(time.Second))

	return seconds, nanoseconds
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}

	return string(encoded)
}
