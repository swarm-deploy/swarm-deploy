package notifiers

import (
	"context"
)

type Message struct {
	// Payload is the stable source-owned notification contract.
	Payload any `json:",inline"`
}

type Notifier interface {
	// Name returns the stable subscriber name.
	Name() string
	// Kind returns the delivery mechanism name.
	Kind() string
	// Notify renders and delivers one notification.
	Notify(ctx context.Context, event Message) error
}
