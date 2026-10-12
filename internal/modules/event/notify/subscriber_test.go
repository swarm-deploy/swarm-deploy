package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notifications"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"go.uber.org/mock/gomock"
)

func TestSubscriberReturnsDeliveryFailureWithoutPublishing(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{"success", nil}, {"retry", errors.New("unavailable")}} {
		t.Run(tc.name, func(t *testing.T) {
			notifier := notifiers.NewMockNotifier(gomock.NewController(t))
			event := &events.DeploySuccess{}
			notifier.EXPECT().Notify(gomock.Any(), notifiers.Message{Payload: eventnotifications.DeploymentPayload{
				ID: "source", Type: "deploySuccess", Status: eventnotifications.StatusSuccess,
				OccurredAt: time.Time{}.UTC(),
			}}).Return(tc.err)
			err := NewSubscriber(notifier).Handle(context.Background(), events.Envelope{ID: "source", Event: event})
			require.ErrorIs(t, err, tc.err)
		})
	}
}
