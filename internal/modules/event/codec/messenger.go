package codec

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

const maxMessengerObservationLength = 256

type messengerObservationPayload struct {
	ResourceID  string `json:"resource_id"`
	ErrorCode   string `json:"error_code,omitempty"`
	Description string `json:"description,omitempty"`
}

func encodeMessengerObservation(resourceID, errorCode, description string) ([]byte, error) {
	payload := messengerObservationPayload{
		ResourceID: resourceID, ErrorCode: errorCode, Description: description,
	}
	if err := validateMessengerObservation(payload); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func decodeMessengerObservation(
	payload []byte, event func(messengerObservationPayload) events.Event,
) (events.Event, error) {
	var value messengerObservationPayload
	if err := decode(payload, &value); err != nil {
		return nil, err
	}
	if err := validateMessengerObservation(value); err != nil {
		return nil, err
	}
	return event(value), nil
}

func isMessengerObservationType(typ events.TypeName) bool {
	return typ == events.TypeNameMessengerUnavailable || typ == events.TypeNameMessengerAvailable ||
		typ == events.TypeNameMessengerMisconfigured || typ == events.TypeNameMessengerDeliverySucceeded
}

func decodeMessengerEvent(typ events.TypeName, payload []byte) (events.Event, error) {
	return decodeMessengerObservation(payload, func(p messengerObservationPayload) events.Event {
		switch typ { //nolint:exhaustive // isMessengerObservationType restricts the accepted types.
		case events.TypeNameMessengerUnavailable:
			return &events.MessengerUnavailable{
				ResourceID: p.ResourceID, ErrorCode: p.ErrorCode, Description: p.Description,
			}
		case events.TypeNameMessengerAvailable:
			return &events.MessengerAvailable{ResourceID: p.ResourceID}
		case events.TypeNameMessengerMisconfigured:
			return &events.MessengerMisconfigured{
				ResourceID: p.ResourceID, ErrorCode: p.ErrorCode, Description: p.Description,
			}
		default:
			return &events.MessengerDeliverySucceeded{ResourceID: p.ResourceID}
		}
	})
}

func validateMessengerObservation(payload messengerObservationPayload) error {
	if strings.TrimSpace(payload.ResourceID) == "" {
		return errors.New("messenger resource ID is required")
	}
	if len(payload.ResourceID) > maxMessengerObservationLength ||
		len(payload.ErrorCode) > maxMessengerObservationLength ||
		len(payload.Description) > maxMessengerObservationLength {
		return errors.New("messenger observation exceeds size limit")
	}
	return nil
}
