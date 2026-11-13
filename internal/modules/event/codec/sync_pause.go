package codec

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

type syncPausePayload struct {
	Stack     string     `json:"stack"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	HasExpiry bool       `json:"has_expiry"`
}

func encodeSyncPause(event events.SyncPauseEvent) ([]byte, error) {
	if err := validateSyncPause(event); err != nil {
		return nil, err
	}
	payload := syncPausePayload{Stack: event.StackName, ExpiresAt: event.ExpiresAt, HasExpiry: event.ExpiresAt != nil}
	return json.Marshal(payload)
}

func decodeSyncPause(typ events.TypeName, payload []byte) (events.Event, error) {
	var value syncPausePayload
	if err := decode(payload, &value); err != nil {
		return nil, err
	}
	if !value.HasExpiry {
		value.ExpiresAt = nil
	}
	contract := events.SyncPauseEvent{StackName: value.Stack, ExpiresAt: value.ExpiresAt}
	if err := validateSyncPause(contract); err != nil {
		return nil, err
	}
	switch typ { //nolint:exhaustive // Caller routes only the three Pause Sync event types here.
	case events.TypeNameSyncPaused:
		return &events.SyncPaused{SyncPauseEvent: contract}, nil
	case events.TypeNameSyncResumed:
		return &events.SyncResumed{SyncPauseEvent: contract}, nil
	case events.TypeNameSyncPauseExpired:
		return &events.SyncPauseExpired{SyncPauseEvent: contract}, nil
	default:
		return nil, errors.New("unsupported sync pause event type")
	}
}

func validateSyncPause(event events.SyncPauseEvent) error {
	if strings.TrimSpace(event.StackName) == "" {
		return errors.New("sync pause stack is required")
	}
	return nil
}
