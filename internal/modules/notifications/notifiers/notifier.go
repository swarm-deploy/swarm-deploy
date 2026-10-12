package notifiers

import (
	"context"
)

type Message struct {
	// Payload is the stable source-owned notification contract.
	Payload any `json:",inline"`
	// Action selects a transport operation for lifecycle notifications.
	Action Action `json:"-"`
	// TargetMessageID is the original transport message for reply or edit.
	TargetMessageID int64 `json:"-"`
}

// Action is the requested notification operation.
type Action string

const (
	// ActionSend creates a new independent message.
	ActionSend Action = "send"
	// ActionReply creates a reply to an existing message.
	ActionReply Action = "reply"
	// ActionEdit updates an existing message.
	ActionEdit Action = "edit"
)

// Receipt identifies the message accepted by an external messenger.
type Receipt struct {
	// MessageID is the transport message identifier, when available.
	MessageID int64
	// Action is the operation actually accepted by the transport.
	Action Action
}

type Notifier interface {
	// Name returns the stable subscriber name.
	Name() string
	// Kind returns the delivery mechanism name.
	Kind() string
	// Notify renders and delivers one notification.
	Notify(ctx context.Context, event Message) (Receipt, error)
}
