//go:generate mockgen -source=$GOFILE -destination=mocks.go -package=notifiers

package notifiers

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
)

// ErrMessageUnavailable is returned when the message to edit is no longer available in the transport.
var ErrMessageUnavailable = errors.New("message is unavailable")

// Fields is a message payload whose keys are available at the top level of message templates.
type Fields map[string]any

type Message struct {
	// EditMessageID is an identifier of a sent message to edit; ignored by notifiers without edit support.
	EditMessageID string `json:"-"`
	// ReplyToID is an identifier of a sent message to reply to; ignored by notifiers without reply support.
	ReplyToID string `json:"-"`

	Payload any `json:",inline"`
}

type Notifier interface {
	Name() string
	Kind() string
	// Notify delivers the message and returns a receipt of the delivered message.
	Notify(ctx context.Context, message Message) (delivery.Receipt, error)
}
