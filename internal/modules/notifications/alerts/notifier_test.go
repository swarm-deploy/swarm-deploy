package alerts

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
)

func testAlert() model.Alert {
	return model.Alert{
		ID: "a1", Title: "Deployment failed", ResourceType: model.ResourceTypeStack,
		ResourceID: "api", Message: "image missing",
		Resolution: &model.AlertResolution{Message: "Deployment completed successfully"},
	}
}

func TestNotifierSendMode(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	store, transport := delivery.NewMockStore(ctrl), delivery.NewMockEditableTransport(ctrl)
	transport.EXPECT().ID().Return("tg").AnyTimes()
	gomock.InOrder(
		transport.EXPECT().Send(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, text string) (delivery.Receipt, error) {
			assert.Contains(t, text, "Alert opened", "opened text")
			return delivery.Receipt{"id": "1"}, nil
		}),
		transport.EXPECT().Send(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, text string) (delivery.Receipt, error) {
			assert.Contains(t, text, "Alert resolved", "resolved text")
			return nil, nil
		}),
	)

	n := NewNotifier(config.AlertNotificationModeSend, []Channel{{Transport: transport}}, delivery.NewService(store, 0))
	require.NoError(t, n.AlertOpened(ctx, testAlert()), "opened")
	require.NoError(t, n.AlertResolved(ctx, testAlert()), "resolved")
}

func TestNotifierEditMode(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	store, transport := delivery.NewMockStore(ctrl), delivery.NewMockEditableTransport(ctrl)
	transport.EXPECT().ID().Return("tg").AnyTimes()
	receipt := delivery.Receipt{"id": "1"}

	store.EXPECT().Get(ctx, "alert:a1", "tg").Return(delivery.Correlation{}, false, nil)
	transport.EXPECT().Send(ctx, gomock.Any()).Return(receipt, nil)
	store.EXPECT().Put(ctx, gomock.Any()).Return(nil)
	stored := delivery.Correlation{CorrelationKey: "alert:a1", ChannelID: "tg", Receipt: receipt}
	store.EXPECT().Get(ctx, "alert:a1", "tg").Return(stored, true, nil)
	transport.EXPECT().Edit(ctx, receipt, gomock.Any()).Return(nil)
	store.EXPECT().Delete(ctx, "alert:a1", "tg").Return(nil)

	n := NewNotifier(config.AlertNotificationModeEdit, []Channel{{Transport: transport}}, delivery.NewService(store, 0))
	require.NoError(t, n.AlertOpened(ctx, testAlert()), "opened")
	require.NoError(t, n.AlertResolved(ctx, testAlert()), "resolved edits the opened message")
}

func TestNotifierChannelErrorsDoNotBlockOthers(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	store := delivery.NewMockStore(ctrl)
	broken, healthy := delivery.NewMockEditableTransport(ctrl), delivery.NewMockEditableTransport(ctrl)
	broken.EXPECT().ID().Return("broken").AnyTimes()
	healthy.EXPECT().ID().Return("healthy").AnyTimes()
	broken.EXPECT().Send(ctx, gomock.Any()).Return(nil, errors.New("telegram down"))
	healthy.EXPECT().Send(ctx, gomock.Any()).Return(nil, nil)

	n := NewNotifier(config.AlertNotificationModeSend,
		[]Channel{{Transport: broken}, {Transport: healthy}}, delivery.NewService(store, 0))

	err := n.AlertOpened(ctx, testAlert())
	require.Error(t, err, "error is reported to the caller")
	assert.Contains(t, err.Error(), "telegram down", "error text")
}

func TestNotifierCustomTemplate(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	transport := delivery.NewMockEditableTransport(ctrl)
	transport.EXPECT().ID().Return("tg").AnyTimes()
	transport.EXPECT().Send(ctx, "api: image missing").Return(nil, nil)

	n := NewNotifier(config.AlertNotificationModeSend,
		[]Channel{{Transport: transport, Message: "{{.alert.ResourceID}}: {{.alert.Message}}"}},
		delivery.NewService(delivery.NewMockStore(ctrl), 0))
	require.NoError(t, n.AlertOpened(ctx, testAlert()), "opened")
}
