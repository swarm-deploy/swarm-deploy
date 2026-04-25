package events

import (
	"encoding/json"
	"strings"
	"time"
)

// TypeName is an unique machine-readable event type identifier.
type TypeName string

// Severity is an event priority level.
type Severity string

// Category is an event functional group.
type Category string

const (
	TypeNameDeploySuccess                    TypeName = "deploySuccess"
	TypeNameDeployFailed                     TypeName = "deployFailed"
	TypeNameSendNotificationFailed           TypeName = "sendNotificationFailed"
	TypeNameSyncManualStarted                TypeName = "syncManualStarted"
	TypeNameServiceReplicasIncreased         TypeName = "serviceReplicasIncreased"
	TypeNameServiceReplicasDecreased         TypeName = "serviceReplicasDecreased"
	TypeNameServiceRestarted                 TypeName = "serviceRestarted"
	TypeNameUserAuthenticated                TypeName = "userAuthenticated"
	TypeNameAssistantPromptInjectionDetected TypeName = "assistantPromptInjectionDetected"
)

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
	SeverityAlert Severity = "alert"
)

const (
	CategorySync     Category = "sync"
	CategorySecurity Category = "security"
)

const (
	defaultSyncDedupWindow   = 5 * time.Second
	serviceDedupWindow       = 15 * time.Second
	securityAlertDedupWindow = 30 * time.Second
)

// Type describes event name and attached metadata.
type Type struct {
	name     TypeName
	severity Severity
	category Category
	window   time.Duration
}

type Event interface {
	// Type returns unique event type identifier.
	Type() Type
	// Message returns short human-readable event description.
	Message() string
	// Details returns event-specific details for history and notifications.
	Details() map[string]string
}

var (
	TypeDeploySuccess = Type{
		name:     TypeNameDeploySuccess,
		severity: SeverityInfo,
		category: CategorySync,
		window:   1 * time.Minute,
	}
	TypeDeployFailed = Type{
		name:     TypeNameDeployFailed,
		severity: SeverityAlert,
		category: CategorySync,
		window:   1 * time.Minute,
	}
	TypeSendNotificationFailed = Type{
		name:     TypeNameSendNotificationFailed,
		severity: SeverityError,
		category: CategorySync,
		window:   1 * time.Hour,
	}
	TypeSyncManualStarted = Type{
		name:     TypeNameSyncManualStarted,
		severity: SeverityInfo,
		category: CategorySync,
		window:   defaultSyncDedupWindow,
	}
	TypeServiceReplicasIncreased = Type{
		name:     TypeNameServiceReplicasIncreased,
		severity: SeverityInfo,
		category: CategorySync,
		window:   serviceDedupWindow,
	}
	TypeServiceReplicasDecreased = Type{
		name:     TypeNameServiceReplicasDecreased,
		severity: SeverityInfo,
		category: CategorySync,
		window:   serviceDedupWindow,
	}
	TypeServiceRestarted = Type{
		name:     TypeNameServiceRestarted,
		severity: SeverityInfo,
		category: CategorySync,
		window:   serviceDedupWindow,
	}
	TypeUserAuthenticated = Type{
		name:     TypeNameUserAuthenticated,
		severity: SeverityInfo,
		category: CategorySecurity,
		window:   1 * time.Minute,
	}
	TypeAssistantPromptInjectionDetected = Type{
		name:     TypeNameAssistantPromptInjectionDetected,
		severity: SeverityAlert,
		category: CategorySecurity,
		window:   securityAlertDedupWindow,
	}

	Types = []Type{
		TypeDeploySuccess,
		TypeDeployFailed,
		TypeSendNotificationFailed,
		TypeSyncManualStarted,
		TypeServiceReplicasIncreased,
		TypeServiceReplicasDecreased,
		TypeServiceRestarted,
		TypeUserAuthenticated,
		TypeAssistantPromptInjectionDetected,
	}
)

// Name returns unique machine-readable event name.
func (t Type) Name() TypeName {
	return t.name
}

// Severity returns event priority.
func (t Type) Severity() Severity {
	return t.severity
}

// Category returns event functional group.
func (t Type) Category() Category {
	return t.category
}

// Window returns deduplication interval for events with identical details.
func (t Type) Window() time.Duration {
	return t.window
}

func (t Type) String() string {
	return string(t.name)
}

// MarshalJSON encodes type as a string.
func (t Type) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

// UnmarshalJSON decodes type from JSON string.
func (t *Type) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	parsed, ok := ParseType(strings.TrimSpace(raw))
	if !ok {
		*t = Type{name: TypeName(strings.TrimSpace(raw))}
		return nil
	}

	*t = parsed
	return nil
}

func (n TypeName) Valid() bool {
	switch n {
	case TypeNameDeploySuccess:
		return true
	case TypeNameDeployFailed:
		return true
	case TypeNameSendNotificationFailed:
		return true
	case TypeNameSyncManualStarted:
		return true
	case TypeNameServiceReplicasIncreased:
		return true
	case TypeNameServiceReplicasDecreased:
		return true
	case TypeNameServiceRestarted:
		return true
	case TypeNameUserAuthenticated:
		return true
	case TypeNameAssistantPromptInjectionDetected:
		return true
	default:
		return false
	}
}

// ParseType resolves event type metadata by event name.
func ParseType(name string) (Type, bool) {
	switch TypeName(name) {
	case TypeNameDeploySuccess:
		return TypeDeploySuccess, true
	case TypeNameDeployFailed:
		return TypeDeployFailed, true
	case TypeNameSendNotificationFailed:
		return TypeSendNotificationFailed, true
	case TypeNameSyncManualStarted:
		return TypeSyncManualStarted, true
	case TypeNameServiceReplicasIncreased:
		return TypeServiceReplicasIncreased, true
	case TypeNameServiceReplicasDecreased:
		return TypeServiceReplicasDecreased, true
	case TypeNameServiceRestarted:
		return TypeServiceRestarted, true
	case TypeNameUserAuthenticated:
		return TypeUserAuthenticated, true
	case TypeNameAssistantPromptInjectionDetected:
		return TypeAssistantPromptInjectionDetected, true
	default:
		return Type{}, false
	}
}

// ParseSeverity decodes severity from text.
func ParseSeverity(raw string) (Severity, bool) {
	switch Severity(raw) {
	case SeverityInfo:
		return SeverityInfo, true
	case SeverityWarn:
		return SeverityWarn, true
	case SeverityError:
		return SeverityError, true
	case SeverityAlert:
		return SeverityAlert, true
	default:
		return "", false
	}
}

// ParseCategory decodes category from text.
func ParseCategory(raw string) (Category, bool) {
	switch Category(raw) {
	case CategorySync:
		return CategorySync, true
	case CategorySecurity:
		return CategorySecurity, true
	default:
		return "", false
	}
}
