package notifiers

import (
	"context"
)

type Message struct {
	Payload any `json:",inline"`
}

type Notifier interface {
	Name() string
	Kind() string
	Notify(ctx context.Context, event Message) error
}
