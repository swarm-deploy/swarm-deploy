package codec

import (
	"encoding/json"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func encodeNotificationDelivery(event events.Event) ([]byte, bool, error) {
	var payload notificationPayload
	switch delivery := event.(type) {
	case *events.SendNotificationFailed:
		payload = notificationPayload{
			EventType: delivery.EventType, Destination: delivery.Destination, Channel: delivery.Channel,
			DestinationKey: delivery.DestinationKey, Error: faults.SafeDiagnostic(delivery.Error),
		}
	case *events.NotificationSent:
		payload = notificationPayload{
			EventType: delivery.EventType, Destination: delivery.Destination, Channel: delivery.Channel,
			DestinationKey: delivery.DestinationKey,
		}
	default:
		return nil, false, nil
	}
	encoded, err := json.Marshal(payload)
	return encoded, true, err
}

func decodeNotificationDelivery(typ events.TypeName, payload []byte) (events.Event, error) {
	return decodeAs(payload, func(stored notificationPayload) events.Event {
		if typ == events.TypeNameNotificationSent {
			return &events.NotificationSent{
				EventType: stored.EventType, Destination: stored.Destination, Channel: stored.Channel,
				DestinationKey: stored.DestinationKey,
			}
		}
		var deliveryErr error
		if stored.Error != "" {
			deliveryErr = errors.New(stored.Error)
		}
		return &events.SendNotificationFailed{
			EventType: stored.EventType, Destination: stored.Destination, Channel: stored.Channel,
			DestinationKey: stored.DestinationKey, Error: deliveryErr,
		}
	})
}
