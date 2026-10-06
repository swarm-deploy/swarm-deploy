package notifiers

import (
	"context"
)

type Image struct {
	FullName string `json:"full_name"`
	Version  string `json:"version"`
}

type Message struct {
	Payload any `json:",inline"`
}

type Notifier interface {
	Name() string
	Kind() string
	Notify(ctx context.Context, event Message) error
}
