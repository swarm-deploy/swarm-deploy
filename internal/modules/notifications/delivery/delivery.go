//go:generate mockgen -source=$GOFILE -destination=mocks.go -package=delivery

// Package delivery sends messages through transports and remembers sent messages so they can be edited later.
// It knows nothing about alerts or events: callers provide an opaque correlation key.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// DefaultTTL is how long a correlation is kept when no explicit TTL is configured.
const DefaultTTL = 24 * time.Hour

// ErrReceiptInvalid is returned by Editor when the sent message can no longer be edited (deleted, too old).
var ErrReceiptInvalid = errors.New("receipt is no longer valid")

// Receipt contains transport-specific data required to edit a previously sent message.
type Receipt map[string]string

// Transport sends messages to one channel.
type Transport interface {
	// ID returns a stable channel identifier used in the correlation key.
	ID() string
	// Send delivers text and returns a receipt; the receipt may be empty if the transport cannot edit.
	Send(ctx context.Context, text string) (Receipt, error)
}

// Editor is an optional transport capability to edit a previously sent message.
type Editor interface {
	// Edit replaces text of the message identified by the receipt.
	Edit(ctx context.Context, receipt Receipt, text string) error
}

// EditableTransport is a transport that can also edit sent messages.
type EditableTransport interface {
	Transport
	Editor
}

// Service delivers messages and edits them by correlation key.
type Service struct {
	store Store
	ttl   time.Duration
	now   func() time.Time
}

// NewService creates a delivery service; a non-positive ttl selects DefaultTTL.
func NewService(store Store, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Service{store: store, ttl: ttl, now: time.Now}
}

// SendTracked sends text and remembers its receipt under key.
// If a live correlation for key and channel already exists the message was already sent and is skipped,
// which makes repeated processing of the same signal idempotent.
func (s *Service) SendTracked(ctx context.Context, transport Transport, key, text string) error {
	_, found, err := s.store.Get(ctx, key, transport.ID())
	if err != nil {
		return fmt.Errorf("get correlation: %w", err)
	}
	if found {
		return nil
	}

	receipt, err := transport.Send(ctx, text)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	if len(receipt) == 0 {
		return nil
	}

	now := s.now()
	err = s.store.Put(ctx, Correlation{
		CorrelationKey: key, ChannelID: transport.ID(), Receipt: receipt, CreatedAt: now, ExpiresAt: now.Add(s.ttl),
	})
	if err != nil {
		// The message is already delivered; losing the correlation only degrades the later edit to a new message.
		slog.ErrorContext(ctx, "[delivery] save correlation", slog.String("channel", transport.ID()), slog.Any("err", err))
	}
	return nil
}

// EditOrSend edits the message remembered under key. When there is no live correlation, the transport
// cannot edit, or the receipt is invalid, text is sent as a separate message.
func (s *Service) EditOrSend(ctx context.Context, transport Transport, key, text string) error {
	correlation, found, err := s.store.Get(ctx, key, transport.ID())
	if err != nil {
		return fmt.Errorf("get correlation: %w", err)
	}

	editor, canEdit := transport.(Editor)
	if !found || !canEdit {
		return s.sendUntracked(ctx, transport, key, text)
	}

	err = editor.Edit(ctx, correlation.Receipt, text)
	switch {
	case err == nil:
		s.deleteCorrelation(ctx, correlation)
		return nil
	case errors.Is(err, ErrReceiptInvalid):
		s.deleteCorrelation(ctx, correlation)
		return s.sendUntracked(ctx, transport, key, text)
	default:
		// Keep the correlation so that a retry can still edit the original message.
		return fmt.Errorf("edit message: %w", err)
	}
}

func (s *Service) sendUntracked(ctx context.Context, transport Transport, key, text string) error {
	if _, err := transport.Send(ctx, text); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	if err := s.store.Delete(ctx, key, transport.ID()); err != nil {
		slog.ErrorContext(ctx, "[delivery] delete correlation", slog.String("channel", transport.ID()), slog.Any("err", err))
	}
	return nil
}

func (s *Service) deleteCorrelation(ctx context.Context, correlation Correlation) {
	if err := s.store.Delete(ctx, correlation.CorrelationKey, correlation.ChannelID); err != nil {
		slog.ErrorContext(ctx, "[delivery] delete correlation",
			slog.String("channel", correlation.ChannelID), slog.Any("err", err))
	}
}
