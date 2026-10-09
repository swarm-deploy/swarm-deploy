package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
)

func testAlert() model.Alert {
	return model.Alert{
		ID: "a1", Title: "Deployment failed", ResourceType: model.ResourceTypeStack,
		ResourceID: "api", Message: "image missing",
		Resolution: &model.AlertResolution{Message: "Deployment completed successfully"},
	}
}

type fixture struct {
	notifier *notifiers.MockNotifier
	store    *delivery.MockStore
	sut      *Notifier
}

func newFixture(t *testing.T, mode config.AlertNotificationMode) fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := fixture{notifier: notifiers.NewMockNotifier(ctrl), store: delivery.NewMockStore(ctrl)}
	f.notifier.EXPECT().Name().Return("tg").AnyTimes()
	f.sut = NewNotifier(mode, []Channel{{ID: "ch1", Notifier: f.notifier}}, f.store, 0)
	return f
}

func fields(t *testing.T, msg notifiers.Message) notifiers.Fields {
	t.Helper()
	f, ok := msg.Payload.(notifiers.Fields)
	require.True(t, ok, "payload carries template fields")
	return f
}

func stored(id string) delivery.Correlation {
	return delivery.Correlation{CorrelationKey: "alert:a1", ChannelID: "ch1", Receipt: delivery.Receipt{MessageID: id}}
}

func TestSendMode(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, config.AlertNotificationModeSend)
	var sent []notifiers.Message
	f.notifier.EXPECT().Notify(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, m notifiers.Message) (delivery.Receipt, error) {
			sent = append(sent, m)
			return delivery.Receipt{MessageID: "1"}, nil
		}).Times(2)

	require.NoError(t, f.sut.AlertOpened(ctx, testAlert()), "opened")
	require.NoError(t, f.sut.AlertResolved(ctx, testAlert()), "resolved")

	require.Len(t, sent, 2, "two separate messages")
	assert.Equal(t, "open", fields(t, sent[0])["status"], "open status")
	assert.Equal(t, "resolved", fields(t, sent[1])["status"], "resolved status")
	assert.Equal(t, "Deployment completed successfully", fields(t, sent[1])["resolution"], "resolution")
	for _, m := range sent {
		assert.Empty(t, m.EditMessageID+m.ReplyToID, "plain messages")
	}
}

func TestEditAndReplyModes(t *testing.T) {
	tests := []struct {
		name string
		mode config.AlertNotificationMode
		want func(m notifiers.Message) (edit, reply string)
	}{
		{"edit", config.AlertNotificationModeEdit, func(m notifiers.Message) (string, string) { return m.EditMessageID, m.ReplyToID }},
		{"reply", config.AlertNotificationModeReply, func(m notifiers.Message) (string, string) { return m.EditMessageID, m.ReplyToID }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, tt.mode)

			f.store.EXPECT().Get(ctx, "alert:a1", "ch1").Return(delivery.Correlation{}, false, nil)
			f.notifier.EXPECT().Notify(ctx, gomock.Any()).Return(delivery.Receipt{MessageID: "10"}, nil)
			f.store.EXPECT().Put(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, c delivery.Correlation) error {
				assert.Equal(t, "alert:a1", c.CorrelationKey, "key")
				assert.Equal(t, "ch1", c.ChannelID, "channel")
				assert.Equal(t, "10", c.Receipt.MessageID, "receipt")
				assert.Equal(t, delivery.DefaultTTL, c.ExpiresAt.Sub(c.CreatedAt), "default ttl")
				return nil
			})
			f.store.EXPECT().Get(ctx, "alert:a1", "ch1").Return(stored("10"), true, nil)
			f.notifier.EXPECT().Notify(ctx, gomock.Any()).DoAndReturn(
				func(_ context.Context, m notifiers.Message) (delivery.Receipt, error) {
					edit, reply := tt.want(m)
					if tt.mode == config.AlertNotificationModeEdit {
						assert.Equal(t, "10", edit, "edits original")
						assert.Empty(t, reply, "no reply")
					} else {
						assert.Equal(t, "10", reply, "replies to original")
						assert.Empty(t, edit, "no edit")
					}
					return delivery.Receipt{MessageID: "10"}, nil
				})
			f.store.EXPECT().Delete(ctx, "alert:a1", "ch1").Return(nil)

			require.NoError(t, f.sut.AlertOpened(ctx, testAlert()), "opened")
			require.NoError(t, f.sut.AlertResolved(ctx, testAlert()), "resolved")
		})
	}
}

func TestOpenedRepeatedProcessingDoesNotDuplicate(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, config.AlertNotificationModeEdit)
	f.store.EXPECT().Get(ctx, "alert:a1", "ch1").Return(stored("10"), true, nil)

	require.NoError(t, f.sut.AlertOpened(ctx, testAlert()), "no second message")
}

func TestResolvedWithoutCorrelationSendsPlainMessage(t *testing.T) {
	for _, mode := range []config.AlertNotificationMode{config.AlertNotificationModeEdit, config.AlertNotificationModeReply} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, mode)
			// A missing or TTL-expired correlation is reported by the store as not found.
			f.store.EXPECT().Get(ctx, "alert:a1", "ch1").Return(delivery.Correlation{}, false, nil)
			f.notifier.EXPECT().Notify(ctx, gomock.Any()).DoAndReturn(
				func(_ context.Context, m notifiers.Message) (delivery.Receipt, error) {
					assert.Empty(t, m.EditMessageID+m.ReplyToID, "plain message")
					return delivery.Receipt{}, nil
				})

			require.NoError(t, f.sut.AlertResolved(ctx, testAlert()), "resolved")
		})
	}
}

func TestEditFallsBackWhenOriginalUnavailable(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, config.AlertNotificationModeEdit)
	f.store.EXPECT().Get(ctx, "alert:a1", "ch1").Return(stored("10"), true, nil)
	gomock.InOrder(
		f.notifier.EXPECT().Notify(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, m notifiers.Message) (delivery.Receipt, error) {
				assert.Equal(t, "10", m.EditMessageID, "edit attempted")
				return delivery.Receipt{}, notifiers.ErrMessageUnavailable
			}),
		f.notifier.EXPECT().Notify(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, m notifiers.Message) (delivery.Receipt, error) {
				assert.Empty(t, m.EditMessageID+m.ReplyToID, "fallback is a plain message")
				return delivery.Receipt{}, nil
			}),
	)
	f.store.EXPECT().Delete(ctx, "alert:a1", "ch1").Return(nil)

	require.NoError(t, f.sut.AlertResolved(ctx, testAlert()), "resolved")
}

func TestResolveAPIErrorKeepsCorrelationAndSendsOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, config.AlertNotificationModeEdit)
	f.store.EXPECT().Get(ctx, "alert:a1", "ch1").Return(stored("10"), true, nil)
	f.notifier.EXPECT().Notify(ctx, gomock.Any()).Return(delivery.Receipt{}, errors.New("telegram down")).Times(1)

	err := f.sut.AlertResolved(ctx, testAlert())
	require.Error(t, err, "error is reported")
	assert.Contains(t, err.Error(), "telegram down", "error text")
}

func TestOpenedErrorsDoNotBlockOtherChannels(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	broken, healthy := notifiers.NewMockNotifier(ctrl), notifiers.NewMockNotifier(ctrl)
	broken.EXPECT().Name().Return("broken").AnyTimes()
	healthy.EXPECT().Name().Return("healthy").AnyTimes()
	broken.EXPECT().Notify(ctx, gomock.Any()).Return(delivery.Receipt{}, errors.New("telegram down"))
	healthy.EXPECT().Notify(ctx, gomock.Any()).Return(delivery.Receipt{}, nil)

	sut := NewNotifier(config.AlertNotificationModeSend,
		[]Channel{{ID: "a", Notifier: broken}, {ID: "b", Notifier: healthy}}, delivery.NewMockStore(ctrl), time.Hour)

	require.Error(t, sut.AlertOpened(ctx, testAlert()), "error is reported")
}

func TestSameNameDifferentDestinationsDoNotConflict(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	first, second := notifiers.NewMockNotifier(ctrl), notifiers.NewMockNotifier(ctrl)
	first.EXPECT().Name().Return("ops").AnyTimes()
	second.EXPECT().Name().Return("ops").AnyTimes()
	store := delivery.NewMockStore(ctrl)
	first.EXPECT().Notify(ctx, gomock.Any()).Return(delivery.Receipt{MessageID: "1"}, nil)
	second.EXPECT().Notify(ctx, gomock.Any()).Return(delivery.Receipt{MessageID: "2"}, nil)
	store.EXPECT().Get(ctx, "alert:a1", "chat-1").Return(delivery.Correlation{}, false, nil)
	store.EXPECT().Get(ctx, "alert:a1", "chat-2").Return(delivery.Correlation{}, false, nil)
	store.EXPECT().Put(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, c delivery.Correlation) error {
		assert.Equal(t, map[string]string{"chat-1": "1", "chat-2": "2"}[c.ChannelID], c.Receipt.MessageID, "per-channel receipt")
		return nil
	}).Times(2)

	sut := NewNotifier(config.AlertNotificationModeEdit,
		[]Channel{{ID: "chat-1", Notifier: first}, {ID: "chat-2", Notifier: second}}, store, 0)
	require.NoError(t, sut.AlertOpened(ctx, testAlert()), "opened")
}
