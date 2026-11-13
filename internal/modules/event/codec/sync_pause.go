package codec

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

type syncPausePayload struct {
	Scope     string     `json:"scope"`
	Stack     string     `json:"stack"`
	Service   string     `json:"service,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	HasExpiry bool       `json:"has_expiry"`
}

func encodeSyncPause(event events.SyncPauseEvent) ([]byte, error) {
	if err := validateSyncPause(event); err != nil {
		return nil, err
	}
	payload := syncPausePayload{
		Scope: event.Scope, Stack: event.StackName, Service: event.ServiceName,
		ExpiresAt: event.ExpiresAt, HasExpiry: event.ExpiresAt != nil,
	}
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
	contract := events.SyncPauseEvent{
		Scope: value.Scope, StackName: value.Stack, ServiceName: value.Service, ExpiresAt: value.ExpiresAt,
	}
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
	switch event.Scope {
	case "stack":
		if event.ServiceName != "" {
			return errors.New("stack pause must not identify a service")
		}
	case "service":
		if strings.TrimSpace(event.ServiceName) == "" {
			return errors.New("service pause requires a service")
		}
	default:
		return errors.New("invalid sync pause scope")
	}
	return nil
}
