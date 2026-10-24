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
	TypeNameServiceCatalogUpdated TypeName = "ServiceCatalogUpdated"
	TypeNameDeploySuccess         TypeName = "DeploySuccess"
	TypeNameAlertOpened           TypeName = "AlertOpened"
	TypeNameAlertResolved         TypeName = "AlertResolved"
	// TypeNameGitPullFailed is an internal durable Git accessibility observation.
	TypeNameGitPullFailed TypeName = "GitPullFailed"
	// TypeNameGitPullSucceeded is an internal durable Git accessibility recovery.
	TypeNameGitPullSucceeded TypeName = "GitPullSucceeded"
	TypeNameDeployFailed     TypeName = "DeployFailed"
	// TypeNameConfigurationChanged is an internal accepted-configuration signal.
	TypeNameConfigurationChanged TypeName = "ConfigurationChanged"
	// TypeNameDeployPreparationFailed is an internal durable lifecycle fact, not a configurable event name.
	TypeNameDeployPreparationFailed TypeName = "DeployPreparationFailed"
	// TypeNameDeployInterrupted is an internal durable lifecycle fact, not a configurable event name.
	TypeNameDeployInterrupted TypeName = "DeployInterrupted"
	// TypeNameOutboxDeliveryFailed is an internal terminal delivery signal.
	TypeNameOutboxDeliveryFailed TypeName = "OutboxDeliveryFailed"
	// TypeNameOutboxDeliveryRecovered is an internal replay recovery signal.
	TypeNameOutboxDeliveryRecovered TypeName = "OutboxDeliveryRecovered"
	// TypeNameOutboxDeliveryDiscarded is an internal operator action signal.
	TypeNameOutboxDeliveryDiscarded          TypeName = "OutboxDeliveryDiscarded"
	TypeNameSyncManualStarted                TypeName = "SyncManualStarted"
	TypeNameWebhookReceived                  TypeName = "WebhookReceived"
	TypeNameNodeJoined                       TypeName = "NodeJoined"
	TypeNameNodeConnected                    TypeName = "NodeConnected"
	TypeNameNodeDisconnected                 TypeName = "NodeDisconnected"
	TypeNameServiceMissed                    TypeName = "ServiceMissed"
	TypeNameServiceReplicasIncreased         TypeName = "ServiceReplicasIncreased"
	TypeNameServiceReplicasDecreased         TypeName = "ServiceReplicasDecreased"
	TypeNameServiceRestarted                 TypeName = "ServiceRestarted"
	TypeNameServicePruned                    TypeName = "ServicePruned"
	TypeNameNetworkCreated                   TypeName = "NetworkCreated"
	TypeNameUserAuthenticated                TypeName = "UserAuthenticated"
	TypeNameAssistantPromptInjectionDetected TypeName = "AssistantPromptInjectionDetected"
)

const legacySendNotificationFailedTypeName TypeName = "SendNotificationFailed"

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
	SeverityAlert Severity = "alert"
)

const (
	CategorySync     Category = "sync"
	CategorySecurity Category = "security"
	CategorySwarm    Category = "swarm"
)

const (
	defaultSyncDedupWindow   = 5 * time.Second
	nodeDedupWindow          = 15 * time.Second
	serviceDedupWindow       = 15 * time.Second
	securityAlertDedupWindow = 30 * time.Second
)

// Type describes event name and attached metadata.
type Type struct {
	name     TypeName
	severity Severity
	category Category
	window   time.Duration
	isPublic bool
}

// Event is domain data published through the event dispatcher.
type Event interface {
	// Type returns unique event type identifier.
	Type() Type
	// Message returns short human-readable event description.
	Message() string
	// Details returns event-specific details for history and notifications.
	Details() map[string]string
}

// Envelope is a dispatcher envelope shared by every subscriber.
type Envelope struct {
	// PublicationSequence is the durable total order assigned when T1 publishes the event.
	PublicationSequence int64
	// OccurredAt is the publication timestamp, preserved across retries.
	OccurredAt time.Time
	// ID uniquely identifies this dispatch in event history and downstream modules.
	ID string
	// Event contains the published domain event.
	Event Event
}

var (
	TypeServiceCatalogUpdated = Type{
		name: TypeNameServiceCatalogUpdated, isPublic: true, severity: SeverityInfo, category: CategorySync,
	}
	TypeDeploySuccess = Type{
		name:     TypeNameDeploySuccess,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySync,
		window:   1 * time.Minute,
	}
	TypeAlertOpened          = Type{name: TypeNameAlertOpened, severity: SeverityAlert, category: CategorySync}
	TypeAlertResolved        = Type{name: TypeNameAlertResolved, severity: SeverityInfo, category: CategorySync}
	TypeGitPullFailed        = Type{name: TypeNameGitPullFailed, severity: SeverityAlert, category: CategorySync}
	TypeGitPullSucceeded     = Type{name: TypeNameGitPullSucceeded, severity: SeverityInfo, category: CategorySync}
	TypeConfigurationChanged = Type{
		name: TypeNameConfigurationChanged, severity: SeverityInfo, category: CategorySync,
	}
	TypeDeployFailed = Type{
		name:     TypeNameDeployFailed,
		severity: SeverityAlert,
		category: CategorySync,
		window:   1 * time.Minute,
	}
	TypeDeployPreparationFailed = Type{
		name: TypeNameDeployPreparationFailed, severity: SeverityAlert, category: CategorySync,
	}
	TypeDeployInterrupted = Type{
		name: TypeNameDeployInterrupted, severity: SeverityAlert, category: CategorySync,
	}
	TypeOutboxDeliveryFailed = Type{
		name: TypeNameOutboxDeliveryFailed, severity: SeverityError, category: CategorySync,
	}
	TypeOutboxDeliveryRecovered = Type{
		name: TypeNameOutboxDeliveryRecovered, severity: SeverityInfo, category: CategorySync,
	}
	TypeOutboxDeliveryDiscarded = Type{
		name: TypeNameOutboxDeliveryDiscarded, severity: SeverityInfo, category: CategorySync,
	}
	TypeSyncManualStarted = Type{
		name:     TypeNameSyncManualStarted,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySync,
		window:   defaultSyncDedupWindow,
	}
	TypeWebhookReceived = Type{
		name:     TypeNameWebhookReceived,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySync,
		// Every authenticated webhook must be recorded without deduplication.
	}
	TypeNodeJoined = Type{
		name:     TypeNameNodeJoined,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySwarm,
	}
	TypeNodeConnected = Type{
		name:     TypeNameNodeConnected,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySwarm,
		window:   nodeDedupWindow,
	}
	TypeNodeDisconnected = Type{
		name:     TypeNameNodeDisconnected,
		isPublic: true,
		severity: SeverityAlert,
		category: CategorySwarm,
		window:   nodeDedupWindow,
	}
	TypeServiceMissed = Type{
		name:     TypeNameServiceMissed,
		isPublic: true,
		severity: SeverityAlert,
		category: CategorySync,
		window:   serviceDedupWindow,
	}
	TypeServiceReplicasIncreased = Type{
		name:     TypeNameServiceReplicasIncreased,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySync,
		window:   serviceDedupWindow,
	}
	TypeServiceReplicasDecreased = Type{
		name:     TypeNameServiceReplicasDecreased,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySync,
		window:   serviceDedupWindow,
	}
	TypeServiceRestarted = Type{
		name:     TypeNameServiceRestarted,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySync,
		window:   serviceDedupWindow,
	}
	TypeServicePruned = Type{
		name:     TypeNameServicePruned,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySync,
		window:   serviceDedupWindow,
	}
	TypeNetworkCreated = Type{
		name:     TypeNameNetworkCreated,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySync,
		window:   defaultSyncDedupWindow,
	}
	TypeUserAuthenticated = Type{
		name:     TypeNameUserAuthenticated,
		isPublic: true,
		severity: SeverityInfo,
		category: CategorySecurity,
		window:   1 * time.Minute,
	}
	TypeAssistantPromptInjectionDetected = Type{
		name:     TypeNameAssistantPromptInjectionDetected,
		isPublic: true,
		severity: SeverityWarn,
		category: CategorySecurity,
		window:   securityAlertDedupWindow,
	}

	Types = []Type{
		TypeAlertOpened,
		TypeAlertResolved,
		TypeGitPullFailed,
		TypeGitPullSucceeded,
		TypeConfigurationChanged,
		TypeDeployFailed,
		TypeDeployPreparationFailed,
		TypeDeployInterrupted,
		TypeOutboxDeliveryFailed,
		TypeOutboxDeliveryRecovered,
		TypeOutboxDeliveryDiscarded,

		TypeServiceCatalogUpdated,
		TypeDeploySuccess,
		TypeSyncManualStarted,
		TypeWebhookReceived,
		TypeNodeJoined,
		TypeNodeConnected,
		TypeNodeDisconnected,
		TypeServiceMissed,
		TypeServiceReplicasIncreased,
		TypeServiceReplicasDecreased,
		TypeServiceRestarted,
		TypeServicePruned,
		TypeNetworkCreated,
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

// IsPublic reports whether this event is available in user-configurable notifications.
// Event History has an independent subscription and filtering policy.
func (t Type) IsPublic() bool {
	return t.isPublic
}

// Valid reports whether the name is allowed in user-configurable notifications.
func (n TypeName) Valid() bool {
	typ, ok := ParseType(string(n))
	return ok && typ.IsPublic()
}

// ParseType resolves metadata for current runtime types and retained historical types.
func ParseType(name string) (Type, bool) {
	if typ, ok := ParseRuntimeType(name); ok {
		return typ, true
	}
	// Kept outside Types so historical Event History rows remain readable
	// without restoring publication, subscriptions, or public configuration.
	if name == string(legacySendNotificationFailedTypeName) {
		return Type{
			name: legacySendNotificationFailedTypeName, severity: SeverityError, category: CategorySync,
		}, true
	}
	return Type{}, false
}

// ParseRuntimeType resolves only event types supported by current publishers and subscribers.
func ParseRuntimeType(name string) (Type, bool) {
	for _, typ := range Types {
		if string(typ.Name()) == name {
			return typ, true
		}
	}
	return Type{}, false
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
	case CategorySwarm:
		return CategorySwarm, true
	default:
		return "", false
	}
}
