package events

import (
	"fmt"
	"time"
)

// SyncPauseEvent is the safe durable contract shared by pause lifecycle events.
type SyncPauseEvent struct {
	// StackName identifies the GitOps stack.
	StackName string
	// ExpiresAt is nil for a pause without expiration and for explicit resume.
	ExpiresAt *time.Time
}

func (e SyncPauseEvent) details() map[string]string {
	details := map[string]string{AttributeStackName: e.StackName}
	if e.ExpiresAt != nil {
		details["expires_at"] = e.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	return details
}

// SyncPaused reports creation or expiration change of a direct pause.
type SyncPaused struct{ SyncPauseEvent }

// Type returns the durable event type.
func (*SyncPaused) Type() Type { return TypeSyncPaused }

// Message returns a safe summary.
func (e *SyncPaused) Message() string { return fmt.Sprintf("Sync paused for stack %s", e.StackName) }

// Details returns target and expiration metadata.
func (e *SyncPaused) Details() map[string]string { return e.details() }

// SyncResumed reports explicit removal of a direct pause.
type SyncResumed struct{ SyncPauseEvent }

// Type returns the durable event type.
func (*SyncResumed) Type() Type { return TypeSyncResumed }

// Message returns a safe summary.
func (e *SyncResumed) Message() string { return fmt.Sprintf("Sync resumed for stack %s", e.StackName) }

// Details returns target metadata.
func (e *SyncResumed) Details() map[string]string { return e.details() }

// SyncPauseExpired reports natural expiration removed during an ordinary sync.
type SyncPauseExpired struct{ SyncPauseEvent }

// Type returns the durable event type.
func (*SyncPauseExpired) Type() Type { return TypeSyncPauseExpired }

// Message returns a safe summary.
func (e *SyncPauseExpired) Message() string {
	return fmt.Sprintf("Sync pause expired for stack %s", e.StackName)
}

// Details returns target and original expiration metadata.
func (e *SyncPauseExpired) Details() map[string]string { return e.details() }
